package main

import (
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
	"github.com/vivainio/walker"
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
	InputPaths  []string
	OutputDir   string
	BuildCmd    string
	Ignores     []string
	ArchiveRoot string
	GitRoot	string
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

func shouldIgnore(pth string, ignores []string) bool {
	for _, ign := range ignores {
		if strings.HasSuffix(pth, ign) {
			return true
		}
	}
	return false
}

func collectWithGit(startPath string) DirEntries{
	cmd := exec.Command("git", "ls-files")
	
	cmd.Dir = startPath
	out, err := cmd.Output()
	if (err != nil) {
		panic(err)
	}
	asStr := string(out)
	lines := strings.Split(asStr, "\n")
	var all DirEntries
	
	for _, v := range lines {
		all = append(all, DirEntry { pth: path.Join(startPath ,v)})
	}
	return all
}

func collectFiles(startPath string, subPaths []string, ignores []string) DirEntries {
	var all DirEntries

	cb := func(pth string, files []os.FileInfo) bool {
		if shouldIgnore(pth, ignores) {
			return false
		}

		for _, value := range files {
			fullpath := pth + "/" + value.Name()
			if !shouldIgnore(fullpath, ignores) {
				all = append(all, DirEntry{fullpath, value, ""})
			}
		}
		return true
	}
	for _, subpath := range subPaths {
		fullpth := startPath + "/" + subpath
		fi, err := os.Stat(fullpth)
		if err != nil {
			fmt.Printf("Cannot open file %s", fullpth)
		}
		if fi.IsDir() {
			walker.WalkOne(fullpth, cb)
		} else {
			all = append(all, DirEntry{fullpth, fi, ""})
		}
	}

	// special usage with --treehash: with empty subpaths, just crawl the rootpath (for --treehash)
	if len(subPaths) == 0 {
		walker.WalkOne(startPath, cb)
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
	if len(config.GitRoot) > 0 {
		return collectWithGit(config.GitRoot)
	}
	return collectFiles(config.InputRoot, config.InputPaths, config.Ignores)
}

//func getCheckSumForFiles(path string, subpaths []string, ignores []string) (DirEntries, string) {
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
	manifestSum := md5.Sum(manifest.Bytes())
	return all, hex.EncodeToString(manifestSum[:])
}

func zipOutput(path string, zipfile string) {
	cmd := exec.Command("7za", "a", zipfile, path+"/*")
	err := cmd.Run()
	if err != nil {
		panic(err)
	}
}

func unzipOutput(path string, zipfile string) {
	// we will replace the old path completely
	os.RemoveAll(path)
	out, err := exec.Command("7za", "x", zipfile, "-o"+path).CombinedOutput()
	if err != nil {
		fmt.Printf("%s", string(out))
		panic(err)
	}
}

func runBuildCommand(config *AppConfig) {
	fmt.Printf("Running build command '%s' in %s\n", config.BuildCmd, config.InputRoot)
	cmd := exec.Command(config.BuildCmd)
	cmd.Dir = config.InputRoot
	out, err := cmd.CombinedOutput()
	fmt.Println(string(out))
	if err != nil {
		fmt.Printf("Build failed with error!")
		panic(err)
	}
}

func buildWithConfig(config *AppConfig) {
	// check input checksum

	archiveRoot := config.ArchiveRoot

	if archiveRoot == "" {
		fmt.Println("HASHIBUILD_ARCHIVE not set, building without artifact caching")
		runBuildCommand(config)
		return
	}

	_, inputChecksum := getCheckSumForFiles(config)
	// if finding archive found, unzip it and we are ready
	zipName := archiveRoot + "/" + config.Name + "_" + inputChecksum + ".zip"
	if _, err := os.Stat(zipName); !os.IsNotExist(err) {
		fmt.Printf("Unzip %s to %s\n", zipName, config.OutputDir)
		unzipOutput(config.OutputDir, zipName)
		return
	}
	// run build if mismatch

	runBuildCommand(config)
	// zip the results
	fmt.Printf("Zipping %s to %s\n", config.OutputDir, zipName)
	ensureDir(archiveRoot)
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
		fmt.Printf("Creating dir %s", pth)
		os.MkdirAll(pth, 0777)
	}	
}

func parseConfig(configPath string) AppConfig {
	cont, err := ioutil.ReadFile(configPath)
	if err != nil {
		panic(err)
	}
	config := AppConfig{"", "", []string{}, "", "", []string{}, "", ""}
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

	if len(os.Args) < 2 {
		flag.Usage()
		return
	}

	flag.Parse()

	var config AppConfig
	if (*toParse) != "" {
		config = parseConfig(*toParse)
		if *archiveDir != "" {
			config.ArchiveRoot = *archiveDir
		}
		if config.ArchiveRoot == "" {
			config.ArchiveRoot = os.Getenv("HASHIBUILD_ARCHIVE")
		}
	}
	if *manifest {
		dumpManifest(&config)
	}

	if *startBuild {
		buildWithConfig(&config)
	}

	if len(*treeHash) > 0 {
		pth, _ := filepath.Abs(*treeHash)
		
		config := AppConfig{GitRoot: pth, InputRoot: *treeHash, InputPaths: []string{}, Ignores: []string{".git", "node_modules"}}
		dumpManifest(&config)
	}

}
