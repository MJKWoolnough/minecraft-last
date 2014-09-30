package main

import (
	"archive/zip"
	"crypto/sha1"
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
	sigExt           = ".sha"
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
	Name    string              `json:"name"`
	Rules   []Rule              `json:"rules"`
	Natives map[string]string   `json:"natives"`
	Extract map[string][]string `json:"extract"`
}

type LaunchConfig struct {
	Args      string    `json:"minecraftArguments"`
	Libraries []Library `json:"libraries"`
	Class     string    `json:"mainClass"`
}

var hex = [...]byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

func fail(test bool, format string, args ...interface{}) {
	if test {
		fmt.Fprintf(os.Stderr, format, args...)
		os.Exit(0)
	}
}

func main() {
	usr, err := user.Current()
	fail(err != nil, "failed to get current user: %s\n", err)

	minecraftDir := flag.String("minecraft", path.Join(usr.HomeDir, ".minecraft"), "Path to minecraft directory")
	debug := flag.Bool("debug", false, "Set to show command on launch")
	profile := flag.String("profile", "", "Selected profile to launch")
	user := flag.String("user", "", "Selected user to launch with")
	lastProfile := flag.Bool("lastprofile", false, "Launch last used profile")
	lastUser := flag.Bool("lastuser", false, "Launch with last used user profile")
	flag.Parse()

	err = os.Chdir(*minecraftDir)
	fail(err != nil, "failed to change to minecraft directory: %s\n", err)

	f, err := os.Open(path.Join(*minecraftDir, launcherProfiles))
	fail(err != nil, "failed to open launcher profiles: %s\n", launcherProfiles, err)
	profileData := new(ProfileData)
	err = json.NewDecoder(f).Decode(profileData)
	fail(err != nil, "failed to decode profiles: %s\n", err)

	f.Close()

	if *lastUser {
		*user = profileData.SelectedUser
	} else {
		for uuid, u := range profileData.Users {
			if u.Name == *user {
				*user = uuid
				break
			}
		}
	}
	if _, ok := profileData.Users[*user]; !ok {
		fmt.Fprint(os.Stderr, "incorrect or no user selected, please choose one of the following: -\n")
		for uuid, u := range profileData.Users {
			ext := ""
			if uuid == profileData.SelectedUser {
				ext = " (-lastuser)"
			}
			fmt.Fprintf(os.Stderr, "	%s%s\n", u.Name, ext)
		}
		os.Exit(0)
	}

	if *lastProfile {
		*profile = profileData.SelectedProfile
	}
	if _, ok := profileData.Profiles[*profile]; !ok {
		fmt.Fprint(os.Stderr, "incorrect or no profile selected, please choose one of the following: -\n")
		for p := range profileData.Profiles {
			ext := ""
			if p == profileData.SelectedProfile {
				ext = " (-lastprofile)"
			}
			fmt.Fprintf(os.Stderr, "	%s%s\n", p, ext)
		}
		os.Exit(0)
	}

	versionDir := path.Join(*minecraftDir, versionsDir, profileData.Profiles[*profile].ID)

	f, err = os.Open(path.Join(versionDir, profileData.Profiles[*profile].ID+jsonExt))
	fail(err != nil, "failed to open profile configuration: %s\n", err)

	launchConfig := new(LaunchConfig)
	err = json.NewDecoder(f).Decode(launchConfig)
	f.Close()
	fail(err != nil, "failed to decode profile configuration: %s\n", err)

	osStr := runtime.GOOS
	switch osStr {
	case "darwin":
		osStr = "osx"
	}

	nativesDir := path.Join(versionDir, "natives")

	os.Mkdir(nativesDir, 0755)

	libraries := make([]string, 0, len(launchConfig.Libraries))

	hash := sha1.New()

	for _, library := range launchConfig.Libraries {
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
		fail(len(pieces) != 2, "unknown library format: %s\n", library.Name)

		librarySplit := strings.Split(pieces[1], ":")
		pathSplit := append(strings.Split(pieces[0], "."), librarySplit...)
		signature := make([]byte, 40)
		if library.Natives[osStr] != "" {
			filename := strings.Join(librarySplit, "-") + "-" + library.Natives[osStr] + jarExt
			nativeLib := path.Join(path.Join(*minecraftDir, libraryDir), path.Join(append(pathSplit, filename)...))

			sig, err := os.Open(nativeLib + sigExt)
			fail(err != nil, "failed to read native library signature: %s\n", err)

			_, err = io.ReadFull(sig, signature)
			fail(err != nil, "failiure while reading native library signature: %s\n", err)

			sig.Close()

			f, err := os.Open(nativeLib)
			fail(err != nil, "failed to open native library for extraction: %s\n", err)

			n, err := io.Copy(hash, f)
			fail(err != nil, "failure when reading compressed native library: %s\n", err)

			for n, b := range hash.Sum(nil) {
				fail(hex[b>>4] != signature[n<<1] || hex[b&15] != signature[n<<1+1], "signature verification failed on %s, expecting %s\n", filename, signature)
			}

			hash.Reset()

			_, err = f.Seek(0, os.SEEK_SET)
			fail(err != nil, "failure when seeking in the compressed native library: %s\n", err)

			z, err := zip.NewReader(f, n)
			fail(err != nil, "failure when opening compressed native library: %s\n", err)

			excludes := library.Extract["exclude"]
		ZipLoop:
			for _, file := range z.File {
				for _, exclude := range excludes {
					if len(file.Name) >= len(exclude) && file.Name[:len(exclude)] == exclude {
						continue ZipLoop
					}
				}
				df, err := os.Create(path.Join(nativesDir, file.Name))
				fail(err != nil, "failed to create decompressed file: %s\n", err)

				cf, err := file.Open()
				fail(err != nil, "failed to open compressed file: %s\n", err)

				_, err = io.Copy(df, cf)
				fail(err != nil, "failed to decompress file: %s\n", err)
			}
			f.Close()
		} else {
			filename := strings.Join(librarySplit, "-") + jarExt
			lPath := path.Join(path.Join(*minecraftDir, libraryDir), path.Join(append(pathSplit, filename)...))
			libraries = append(libraries, lPath)
		}
	}

	libraries = append(libraries, path.Join(versionDir, profileData.Profiles[*profile].ID+jarExt))

	args := []string{
		"-Xmx1G",
		"-XX:+UseConcMarkSweepGC",
		"-XX:+CMSIncrementalMode",
		"-XX:-UseAdaptiveSizePolicy",
		"-Xmn128M",
		"-Djava.library.path=" + nativesDir,
		"-cp",
		strings.Join(libraries, ":"),
		launchConfig.Class,
	}

	ma := strings.Split(launchConfig.Args, " ")
	for i, arg := range ma {
		switch arg {
		case "${auth_player_name}":
			ma[i] = profileData.Users[*user].Name
		case "${auth_session}":
			ma[i] = "token:" + profileData.Users[*user].AccessToken + ":" + *user
		case "${version_name}":
			ma[i] = profileData.Profiles[*profile].ID
		case "${game_directory}":
			ma[i] = *minecraftDir
		case "${game_assets}":
			ma[i] = path.Join(*minecraftDir, "assets", "virtual", "legacy")
		}
	}
	args = append(args, ma...)
	cmd := exec.Command("java", args...)
	fmt.Printf("Launching Minecraft, %v, with user %s\n", *profile, profileData.Users[*user].Name)
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
	err = os.RemoveAll(nativesDir)
	fail(err != nil, "failed to remove natives directory: %s\n", err)
}
