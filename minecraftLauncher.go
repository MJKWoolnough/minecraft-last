package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path"
	"runtime"
	"strings"
)

const (
	launcherProfiles = "launcher_profiles.json"
	versionsDir      = "versions"
	libraryDir       = "libraries"
	jsonExt          = ".json"
	jarExt           = ".jar"
	allow            = "allow"
)

type Profile struct {
	ID   string `json:"lastVersionId"`
	Args string `json:"javaArgs"`
}

type User struct {
	Name        string `json:"displayName"`
	AccessToken string `json:"accessToken"`
}

type ProfileData struct {
	Profiles        map[string]Profile `json:"profiles"`
	SelectedProfile string             `json:"selectedProfile"`
	Users           map[string]User    `json:"authenticationDatabase"`
	SelectedUser    string             `json:"selectedUser"`
}

type OS struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Rule struct {
	Action string `json:"action"`
	OS     OS     `json:"os"`
}

type Library struct {
	Name    string            `json:"name"`
	Rules   []Rule            `json:"rules"`
	Natives map[string]string `json:"natives"`
}

type LaunchConfig struct {
	Args      string    `json:"minecraftArguments"`
	Libraries []Library `json:"libraries"`
	Class     string    `json:"mainClass"`
}

func main() {
	usr, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get current user: %s\n", err)
		return
	}
	minecraftDir := flag.String("minecraft", path.Join(usr.HomeDir, ".minecraft"), "Path to minecraft directory")
	debug := flag.Bool("debug", false, "Set to show command on launch")
	flag.Parse()

	err = os.Chdir(*minecraftDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to change to minecraft directory: %s\n", err)
		return
	}

	f, err := os.Open(path.Join(*minecraftDir, launcherProfiles))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open launcher profiles: %s\n", launcherProfiles, err)
		return
	}
	profileData := new(ProfileData)
	err = json.NewDecoder(f).Decode(profileData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to decode profiles: %s\n", err)
		return
	}
	f.Close()

	versionDir := path.Join(*minecraftDir, versionsDir, profileData.Profiles[profileData.SelectedProfile].ID)
	fi, err := os.Stat(versionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get information on version directory: %s\n", err)
		return
	} else if !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "version directory is not a directory\n")
		return
	}

	nativesDir := ""
	f, err = os.Open(versionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open version directory: %s\n", err)
		return
	}
	toMatch := profileData.Profiles[profileData.SelectedProfile].ID + "-natives-"
	for {
		fi, err := f.Readdir(1)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "failed to read version directory: %s\n", err)
			return

		}
		if s := fi[0].Name(); len(s) >= len(toMatch) && s[:len(toMatch)] == toMatch {
			nativesDir = s
			break
		}
	}
	if nativesDir == "" {
		fmt.Fprintf(os.Stderr, "failed to find natives directory")
	}

	f, err = os.Open(path.Join(versionDir, profileData.Profiles[profileData.SelectedProfile].ID+jsonExt))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open profile configuration: %s\n", err)
		return
	}
	launchConfig := new(LaunchConfig)
	err = json.NewDecoder(f).Decode(launchConfig)
	f.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to decode profile configuration: %s\n", err)
		return
	}

	osStr := runtime.GOOS
	switch osStr {
	case "darwin":
		osStr = "osx"
	}
	libraries := make([]string, 0, len(launchConfig.Libraries))
	for _, library := range launchConfig.Libraries {
		if library.Natives[osStr] != "" {
			continue
		}
		allowed := len(library.Rules) == 0
		for _, rule := range library.Rules {
			if rule.OS.Name == osStr || rule.OS.Name == "" {
				//version check
				allowed = rule.Action == allow
			}
		}
		if !allowed {
			continue
		}
		pieces := strings.SplitN(library.Name, ":", 2)
		if len(pieces) != 2 {
			fmt.Fprintf(os.Stderr, "unknown library format: %s\n", library.Name)
			return
		}
		librarySplit := strings.Split(pieces[1], ":")
		filename := strings.Join(librarySplit, "-") + jarExt
		pathSplit := append(strings.Split(pieces[0], "."), librarySplit...)
		libraries = append(libraries, path.Join(path.Join(*minecraftDir, libraryDir), path.Join(append(pathSplit, filename)...)))
	}

	libraries = append(libraries, path.Join(versionDir, profileData.Profiles[profileData.SelectedProfile].ID+jarExt))

	args := []string{
		"-Xmx1G",
		"-XX:+UseConcMarkSweepGC",
		"-XX:+CMSIncrementalMode",
		"-XX:-UseAdaptiveSizePolicy",
		"-Xmn128M",
		"-Djava.library.path=" + path.Join(versionDir, nativesDir),
		"-cp",
		strings.Join(libraries, ":"),
		"net.minecraft.launchwrapper.Launch",
	}

	ma := strings.Split(launchConfig.Args, " ")
	for i, arg := range ma {
		switch arg {
		case "${auth_player_name}":
			ma[i] = profileData.Users[profileData.SelectedUser].Name
		case "${auth_session}":
			ma[i] = "token:" + profileData.Users[profileData.SelectedUser].AccessToken + ":" + profileData.SelectedUser
		case "${version_name}":
			ma[i] = profileData.Profiles[profileData.SelectedProfile].ID
		case "${game_directory}":
			ma[i] = *minecraftDir
		case "${game_assets}":
			ma[i] = path.Join(*minecraftDir, "assets", "virtual", "legacy")
		}
	}
	args = append(args, ma...)
	cmd := exec.Command("java", args...)
	fmt.Printf("Launching Minecraft, %v, with user %s\n", profileData.SelectedProfile, profileData.Users[profileData.SelectedUser].Name)
	if *debug {
		fmt.Printf("java")
		for _, arg := range args {
			fmt.Printf(" %q", arg)
		}
		fmt.Println()
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open Stdout pipe: %s\n", err)
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open Stderr pipe: %s\n", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	err = cmd.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error occurred while running: %s", err)
	}
	go io.Copy(os.Stdout, stdout)
	go io.Copy(os.Stderr, stderr)
	cmd.Wait()
}
