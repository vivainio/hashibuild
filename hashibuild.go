package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirEntry is single path entry + checksum
type DirEntry struct {
	pth      string
	fi       os.FileInfo
	checksum string
}

type DirEntries []DirEntry

func (a DirEntries) Len() int           { return len(a) }
func (a DirEntries) Less(i, j int) bool { return a[i].pth < a[j].pth }
func (a DirEntries) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// AppConfig is configuration to pass around
type AppConfig struct {
	Name      string
	InputRoot string
	OutputDir string
	// usually same as inputroot. Doesn't need to be provided if it is
	OutputRoot    string
	BuildCmd      string
	ArchiveLocal  string
	ArchiveRemote string
	Include       []string
	Exclude       []string
	Salt          string
	Uploader      string
	BuildParam    string
}

func countFullChecksum(ents *DirEntries) {
	for i, v := range *ents {
		st, err := os.Stat(v.pth)
		if err != nil {
			fmt.Printf("SKIP bad file [%s]\n", v.pth)
			continue
		}
		v.fi = st

		if v.fi.IsDir() {
			continue
		}

		if v.fi.Size() == 0 {
			v.checksum = "0000"
			continue
		}

		dat, err := ioutil.ReadFile(v.pth)
		if err != nil {
			panic(err)
		}
		var csum = md5.Sum(dat)
		v.checksum = hex.EncodeToString(csum[:])
		//fmt.Printf("%s\n %x", v.checksum, csum)
		(*ents)[i] = v
	}
}

func shouldIgnore(config *AppConfig, pth string) bool {
	pth = strings.ToLower(pth)

	for _, v := range config.Exclude {
		if strings.HasPrefix(pth, strings.ToLower(v)) {
			return true
		}
	}

	if len(config.Include) == 0 {
		return false
	}

	for _, v := range config.Include {
		if strings.HasPrefix(pth, strings.ToLower(v)) {
			return false
		}
	}
	return true
}

func collectWithGit(config *AppConfig) DirEntries {
	cmd := exec.Command("git", "ls-files")

	cmd.Dir = config.InputRoot
	out, err := cmd.Output()
	if err != nil {
		panic(err)
	}
	asStr := string(out)
	lines := strings.Split(asStr, "\n")
	var all DirEntries

	for _, v := range lines {
		if shouldIgnore(config, v) {
			continue
		}
		all = append(all, DirEntry{pth: path.Join(config.InputRoot, v)})
	}
	return all
}

func normalizePaths(ents *DirEntries, rootPath string) {
	for i, v := range *ents {
		oldpath := v.pth
		newpath := strings.Replace(strings.TrimPrefix(oldpath, rootPath+"/"), "\\", "/", -1)
		v.pth = newpath
		(*ents)[i] = v
	}
}

func collectByConfig(config *AppConfig) DirEntries {
	return collectWithGit(config)
}

func getCheckSumForFiles(config *AppConfig) (DirEntries, string) {
	all := collectByConfig(config)
	sort.Sort(all)
	countFullChecksum(&all)
	normalizePaths(&all, config.InputRoot)
	var manifest bytes.Buffer
	for _, v := range all {
		manifest.WriteString(v.pth)
		manifest.WriteString(v.checksum)
		//fmt.Printf("%s %s\n", v.pth, v.checksum)
	}
	if config.Salt != "" {
		manifest.WriteString(config.Salt)
	}

	if config.BuildParam != "" {
		manifest.WriteString(config.BuildParam)
	}

	manifestSum := md5.Sum(manifest.Bytes())
	return all, hex.EncodeToString(manifestSum[:])
}

func run(cwd string, bin string, arg ...string) {
	fmt.Printf("> %s %s", bin, arg)
	cmd := exec.Command(bin, arg...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	fmt.Printf("%s\n", string(out))
	if err != nil {
		fmt.Printf("Command failed, will panic: %s '%s'\n", bin, string(out))
		panic(err)
	}
}

func zipOutput(rootPath string, path string, zipfile string) {
	zipBin := findExe("7za.exe")
	run(rootPath, zipBin, "a", "-y", "-r", zipfile, path)

}

func unzipOutput(pth string, zipfile string) {
	ensureDir(pth)
	unzipBin := findExe("7za.exe")
	run(".", unzipBin, "-y", "x", zipfile, "-o"+pth)
}

func createSpacedCommand(fullCommand string) *exec.Cmd {
	parts := strings.Fields(fullCommand)
	cmd := exec.Command(parts[0], parts[1:]...)
	return cmd
}

func runCommand(config *AppConfig, fullCommand string, ignoreError bool) {
	fmt.Printf("> %s\n", fullCommand)

	cmd := createSpacedCommand(fullCommand)
	cmd.Dir = config.InputRoot
	out, err := cmd.CombinedOutput()
	fmt.Println(string(out))
	if err != nil {
		fmt.Printf("Command '%s' failed with error %s", fullCommand, err)
		if !ignoreError {
			panic(err)
		}
	}

}

func runBuildCommand(config *AppConfig) {
	fmt.Printf("Running build command '%s' in %s\n", config.BuildCmd, config.InputRoot)
	if strings.Contains(config.BuildCmd, "[BUILDPARAM]") {
		fmt.Printf("Warning: build command %s contains [BUILDPARAM], forgot the --buildparam argument to hashibuild invocation?\n", config.BuildCmd)
	}
	runCommand(config, config.BuildCmd, false)
}

func fetchTo(url string, to string) bool {
	fmt.Printf("GET %s\n", url)
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		fmt.Printf("Not available: %s\n", url)
		return false
	}
	defer resp.Body.Close()
	out, err := os.Create(to)
	if err != nil {
		panic(err)
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		panic(err)
	}
	return true
}

func discoverArchive(config *AppConfig, checksum string) (string, bool) {
	archiveRoot := config.ArchiveLocal
	zipName := config.Name + "_" + checksum + ".zip"

	// 1. just try local
	localZipName := filepath.Join(archiveRoot, zipName)
	_, err := os.Stat(localZipName)
	if err == nil {
		return localZipName, true
	}

	// 2. try remote if applicable

	remoteArchive := config.ArchiveRemote

	if remoteArchive == "" {
		return localZipName, false
	}
	if strings.Index(remoteArchive, "[ZIP]") == -1 {
		fmt.Printf("Error: remote archive template %s does not contain [ZIP]\n", remoteArchive)
		return "", false
	}
	remoteUrl := strings.Replace(remoteArchive, "[ZIP]", zipName, -1)
	fetched := fetchTo(remoteUrl, localZipName)
	if !fetched {
		return localZipName, false
	}
	return localZipName, true
}

func buildWithConfig(config *AppConfig) {
	// check input checksum
	fmt.Printf("Config %s\n", config)
	archiveRoot := config.ArchiveLocal

	if archiveRoot == "" {
		fmt.Println("HASHIBUILD_ARCHIVE not set, building without artifact caching")
		runBuildCommand(config)
		return
	}

	_, inputChecksum := getCheckSumForFiles(config)
	// if finding archive found, unzip it and we are ready
	ensureDir(archiveRoot)

	zipName, found := discoverArchive(config, inputChecksum)

	if found {
		fmt.Printf("Unzip %s in %s\n", zipName, config.InputRoot)
		unzipOutput(config.OutputRoot, zipName)
		return
	}

	// run build if mismatch

	runBuildCommand(config)

	// zip the results
	fmt.Printf("Zipping %s to %s\n", config.OutputRoot, zipName)
	zipOutput(config.OutputRoot, config.OutputDir, zipName)
	if config.Uploader != "" {
		uploadCmd := strings.Replace(config.Uploader, "[ZIP]", zipName, -1)
		fmt.Printf("Running uploader command: '%s'\n", uploadCmd)
		runCommand(config, uploadCmd, true)

	}
}

func checkDir(pth string) {
	if _, err := os.Stat(pth); os.IsNotExist(err) {
		fmt.Printf("Path does not exist: %s", pth)
		panic(err)
	}
}

func findExe(exe string) string {
	dir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	adjacent := filepath.Join(dir, exe)
	if _, err := os.Stat(adjacent); err == nil {
		return adjacent
	}
	// if adjacent file doesn't exist, return the basename and assume it's on PATH
	return exe

}

func ensureDir(pth string) {
	if _, err := os.Stat(pth); os.IsNotExist(err) {
		fmt.Printf("Creating dir: %s\n", pth)
		os.MkdirAll(pth, 0777)
	} else {
		fmt.Printf("Path exists: %s\n", pth)
	}
}

func parseConfig(configPath string) AppConfig {
	cont, err := ioutil.ReadFile(configPath)
	if err != nil {
		panic(err)
	}
	config := AppConfig{}
	err = json.Unmarshal(cont, &config)
	if err != nil {
		panic(err)
	}
	// fixup paths to be relative to config file
	configDir, _ := filepath.Abs(filepath.Dir(configPath))
	config.InputRoot = filepath.Join(configDir, config.InputRoot)
	if config.OutputRoot == "" {
		config.OutputRoot = config.InputRoot
	} else {
		config.OutputRoot = filepath.Join(configDir, config.OutputRoot)
	}
	//config.OutputDir = config.OutputDir
	checkDir(config.InputRoot)
	return config
}

func dumpManifest(config *AppConfig) {
	all, csum := getCheckSumForFiles(config)
	for _, v := range all {
		fmt.Printf("%s %s\n", v.pth, v.checksum)
	}
	fmt.Printf("Total: %s\n", csum)
}

// 500mb
const archiveMaxSize = 500 * 1024 * 1024

func vacuumDirectory(pth string) {
	if _, err := os.Stat(pth); os.IsNotExist(err) {
		fmt.Printf("vacuum: path %s does not exist, doing nothing", pth)
		return
	}

	dir, _ := os.Open(pth)
	files, err := dir.Readdir(-1)
	if err != nil {
		panic(err)
	}
	// sort by size so bigger ones get deleted earlier
	sort.Slice(files, func(i int, j int) bool {
		return files[i].Size() > files[j].Size()
	})
	tooOld := time.Now().AddDate(0, 0, -3)
	var cumSize int64

	for _, fi := range files {
		cumSize = cumSize + fi.Size()

		if tooOld.After(fi.ModTime()) || cumSize > archiveMaxSize {
			os.Remove(filepath.Join(pth, fi.Name()))
		}
	}
}

func main() {
	manifest := flag.Bool("manifest", false, "Show manifest (requires --config)")
	treeHash := flag.String("treehash", "", "Show manifest for specified path (no config needed)")
	toParse := flag.String("config", "", "Json config file")
	startBuild := flag.Bool("build", false, "Run build")
	archiveDir := flag.String("archive", "", "Archive root dir (needed if HASHIBUILD_ARCHIVE env var is not set)")
	salt := flag.String("salt", "", "Provide salt string to invalidate hashes that would otherwise be same")
	vacuum := flag.Bool("vacuum", false, "Clean up archive directory from old/big files")
	buildParam := flag.String("buildparam", "", "Provide extra flags to build command, stored in [BUILDPARAM] and added to hash")
	if len(os.Args) < 2 {
		flag.Usage()
		return
	}

	flag.Parse()

	config := AppConfig{}
	if (*toParse) != "" {
		config = parseConfig(*toParse)
	}

	if *archiveDir != "" {
		config.ArchiveLocal = *archiveDir
	}
	if config.ArchiveLocal == "" {
		config.ArchiveLocal = os.Getenv("HASHIBUILD_ARCHIVE")
	}
	if config.ArchiveRemote == "" {
		config.ArchiveRemote = os.Getenv("HASHIBUILD_ARCHIVE_REMOTE")
	}
	if config.Uploader == "" {
		config.Uploader = os.Getenv("HASHIBUILD_UPLOADER")
	}

	if *buildParam != "" {
		config.BuildParam = *buildParam
		config.BuildCmd = strings.Replace(config.BuildCmd, "[BUILDPARAM]", *buildParam, -1)
	}

	config.Salt = *salt

	if *manifest {
		dumpManifest(&config)
	}

	if *startBuild {
		buildWithConfig(&config)
	}

	if *vacuum {
		vacuumDirectory(config.ArchiveLocal)
	}
	if len(*treeHash) > 0 {
		pth, _ := filepath.Abs(*treeHash)
		config := AppConfig{InputRoot: pth, Salt: *salt}
		dumpManifest(&config)
	}

}
