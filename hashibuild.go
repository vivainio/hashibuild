package main

import (
	"io"
	"net/http"
	"path"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)


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

type AppConfig struct {
	Name        string
	InputRoot   string
	OutputDir   string
	BuildCmd    string
	ArchiveLocal string
	ArchiveRemote string
	Include		[]string
	Exclude		[]string
	Salt		string
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
	for _,v := range config.Exclude {
		if strings.HasPrefix(pth, v) {
			return true
		}
	}

	if len(config.Include) == 0 {
		return false
	}

	for _, v := range config.Include {
		if strings.HasPrefix(pth, v) {
			return false
		}
	}
	return true
}


func collectWithGit(config *AppConfig) DirEntries{
	cmd := exec.Command("git", "ls-files")
	
	cmd.Dir = config.InputRoot
	out, err := cmd.Output()
	if (err != nil) {
		panic(err)
	}
	asStr := string(out)
	lines := strings.Split(asStr, "\n")
	var all DirEntries
	
	for _, v := range lines {
		if shouldIgnore(config, v) {
			continue
		}
		all = append(all, DirEntry { pth: path.Join(config.InputRoot ,v)})
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
	
	manifestSum := md5.Sum(manifest.Bytes())
	return all, hex.EncodeToString(manifestSum[:])
}

func run(cwd string, bin string, arg ...string) {
	fmt.Printf("> %s %s", bin, arg)			
	cmd := exec.Command(bin, arg...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()	
	fmt.Printf("%s", string(out))
	if err != nil {
		panic(err)
	}
}


func zipOutput(path string, zipfile string) {
	run(path, "zip", "-r", zipfile, "*")
}

func unzipOutput(pth string, zipfile string) {
	// we will replace the old path completely	
	ensureDir(pth)
	err := os.RemoveAll(pth)
	if err != nil {
		panic(err)
	}
	ensureDir(pth)
	run(".", "unzip", zipfile, "-d"+pth)
}

func runBuildCommand(config *AppConfig) {
	fmt.Printf("Running build command '%s' in %s\n", config.BuildCmd, config.InputRoot)
	parts := strings.Fields(config.BuildCmd)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = config.InputRoot
	out, err := cmd.CombinedOutput()
	fmt.Println(string(out))
	if err != nil {
		fmt.Printf("Build failed with error!")
		panic(err)
	}
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
	_, err  = io.Copy(out, resp.Body)
	if err != nil {
		panic(err)
	}
	return true
}

func discoverArchive(config *AppConfig, checksum string) (string,bool) {
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
		fmt.Printf("Unzip %s to %s\n", zipName, config.OutputDir)
		unzipOutput(config.OutputDir, zipName)
		return
	}
	
	// run build if mismatch

	runBuildCommand(config)

	// zip the results
	fmt.Printf("Zipping %s to %s\n", config.OutputDir, zipName)
	zipOutput(config.OutputDir, zipName)
}

func checkDir(pth string) {
	if _, err := os.Stat(pth); os.IsNotExist(err) {
		fmt.Printf("Path does not exist: %s", pth)
		panic(err)
	}
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
	if (err != nil) {
		panic(err)
	}
	// fixup paths to be relative to config file
	configDir, _ := filepath.Abs(filepath.Dir(configPath))
	config.InputRoot = filepath.Join(configDir, config.InputRoot)
	config.OutputDir = filepath.Join(configDir, config.OutputDir)	
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

func main() {
	manifest := flag.Bool("manifest", false, "Show manifest (requires --config)")
	treeHash := flag.String("treehash", "", "Show manifest for specified path (no config needed)")
	toParse := flag.String("config", "", "Json config file")
	startBuild := flag.Bool("build", false, "Run build")
	archiveDir := flag.String("archive", "", "Archive root dir (needed if HASHIBUILD_ARCHIVE env var is not set)")
	fetch := flag.String("fetch", "", "Fetch remote archive file to local archive")
	salt := flag.String("salt", "", "Provide salt string to invalidate hashes that would otherwise be same")
	if len(os.Args) < 2 {
		flag.Usage()
		return
	}

	flag.Parse()

	var config AppConfig
	if (*toParse) != "" {
		config = parseConfig(*toParse)
		if *archiveDir != "" {
			config.ArchiveLocal = *archiveDir
		}
		if config.ArchiveLocal == "" {
			config.ArchiveLocal = os.Getenv("HASHIBUILD_ARCHIVE")
		}
		if config.ArchiveRemote == "" {
			config.ArchiveRemote = os.Getenv("HASHIBUILD_ARCHIVE_REMOTE")
		}
		config.Salt = *salt
	}

	if *fetch != "" {
		_, inputChecksum := getCheckSumForFiles(&config)
		// if finding archive found, unzip it and we are ready
		zipName, _ := discoverArchive(&config, inputChecksum)
		fmt.Printf("%s", zipName)
	}

	if *manifest {
		dumpManifest(&config)
	}

	if *startBuild {
		buildWithConfig(&config)
	}

	if len(*treeHash) > 0 {
		pth, _ := filepath.Abs(*treeHash)
		
		config := AppConfig{InputRoot: pth, Salt: *salt }
		dumpManifest(&config)
	}

}
