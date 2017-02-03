package main

import (
	"container/list"
	"flag"
	"fmt"
	"github.com/fatih/color"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

type PathConf struct {
	Type string
	Path string
}

func readEntryTags(entryTagsPath string) (entryTags map[string]string) {
	buf, err := ioutil.ReadFile(entryTagsPath)
	if err != nil {
		panic(err)
	}
	err = yaml.Unmarshal(buf, &entryTags)
	if err != nil {
		panic(err)
	}
	for k, v := range entryTags {
		if v == "" {
			entryTags[k] = k
		}
	}
	return
}

func searchPaths(pathConfMap map[string]PathConf) (fullPathMap map[string]string) {
	fullPathMap = make(map[string]string)
	for path, conf := range pathConfMap {
		if conf.Type == "exec" {
			if fullPath, err := exec.LookPath(path); err == nil {
				fullPathMap[path] = fullPath
			}
		} else if conf.Type == "dir" {
			if ft, err := os.Stat(path); err == nil {
				if ft.IsDir() {
					if fullPath, err := filepath.Abs(path); err == nil {
						fullPathMap[path] = fullPath
					}
				}
			}
		} else if conf.Type == "env" {
			env := os.Getenv(conf.Path)
			fullPathMap[path] = env
		} else {
			panic("unknown pathconf type" + conf.Type)
		}
	}
	return
}

func readPathConfs(polkaDirPaths []string) (fullPathMap map[string]string) {
	fullPathMap = make(map[string]string)
	for _, dirPath := range polkaDirPaths {
		confPath := dirPath + "/paths.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			panic(err)
		}
		var pathConfMap map[string]PathConf
		err = yaml.Unmarshal(buf, &pathConfMap)
		if err != nil {
			panic(err)
		}
		for path, fullPath := range searchPaths(pathConfMap) {
			fullPathMap[path] = fullPath
		}
	}
	return
}

func readTagConfs(polkaDirPaths []string) (allTagConfMap map[string]map[string]string) {
	allTagConfMap = make(map[string]map[string]string)
	for _, dirPath := range polkaDirPaths {
		confPath := dirPath + "/tags.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			panic(err)
		}
		var tagConfMap map[string]map[string]string
		err = yaml.Unmarshal(buf, &tagConfMap)
		if err != nil {
			panic(err)
		}
		for tag, children := range tagConfMap {
			for k, v := range children {
				if v == "" {
					children[k] = k
				}
			}
			allTagConfMap[tag] = children // overwrite
		}
	}
	return
}

func resolveTags(tagConf map[string]map[string]string, entryTags map[string]string) (
	acceptTags map[string]string, rejectTags map[string]string) {
	type TagItem struct {
		Tag   string
		Value string
		Depth int
	}

	queue := list.New()
	for k, v := range entryTags {
		queue.PushBack(TagItem{Tag: k, Value: v, Depth: 1})
	}
	acceptTagItems := make(map[string]TagItem)
	rejectTagItems := make(map[string]TagItem)

	// breadth first search
	for queue.Len() > 0 {
		item := queue.Remove(queue.Front()).(TagItem)
		tag, depth := item.Tag, item.Depth

		// remove double negative
		for strings.HasPrefix(tag, "!!") {
			tag = tag[2:]
		}

		if _, ok := acceptTagItems[tag]; ok {
			continue
		} else if _, ok := rejectTagItems[tag]; ok {
			continue
		}

		removeFlag := tag[0] == '!'
		if removeFlag {
			rejectTagItems[tag[1:]] = item
		} else {
			acceptTagItems[tag] = item
		}

		newTags := tagConf[tag]
		for newTag, v := range newTags {
			if removeFlag {
				item := TagItem{Tag: "!" + newTag[1:], Value: v, Depth: depth + 1}
				queue.PushBack(item)
			} else {
				item := TagItem{Tag: newTag, Value: v, Depth: depth + 1}
				queue.PushBack(item)
			}
		}
	}

	// delete intersection
	for k, item := range acceptTagItems {
		if rejectItem, ok := rejectTagItems[k]; ok {
			if rejectItem.Depth > item.Depth { // gt
				delete(rejectTagItems, k)
			}
		}
	}
	for k, item := range rejectTagItems {
		if acceptItem, ok := acceptTagItems[k]; ok {
			if acceptItem.Depth >= item.Depth { // ge
				delete(acceptTagItems, k)
			}
		}
	}

	acceptTags = make(map[string]string)
	rejectTags = make(map[string]string)
	for k, item := range acceptTagItems {
		acceptTags[k] = item.Value
	}
	for k, item := range rejectTagItems {
		rejectTags[k] = item.Value
	}

	return
}

type RawRuleConf struct {
	Dir  string
	Dirs []string
	Pat  string
}

type RuleConf struct {
	Directories []string
	Pattern     *regexp.Regexp
}

func readRuleConfs(polkaDirPaths []string) (ruleConfMap map[string]RuleConf) {
	ruleConfMap = make(map[string]RuleConf)
	for _, dirPath := range polkaDirPaths {
		confPath := dirPath + "/rules.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			panic(err)
		}
		var rawRuleConfMap map[string]RawRuleConf
		err = yaml.Unmarshal(buf, &rawRuleConfMap)
		if err != nil {
			panic(err)
		}
		for k, v := range rawRuleConfMap {
			if v.Dir != "" {
				v.Dirs = append(v.Dirs, v.Dir)
			}
			ruleConfMap[k] = RuleConf{
				Directories: v.Dirs,
				Pattern:     regexp.MustCompile(v.Pat),
			}
		}
	}
	return
}

func toBasenameWithoutExt(path string, recursive bool) (basename string) {
	basename = filepath.Base(path)
	oldlen := len(basename)
	for true {
		basename = strings.TrimSuffix(basename, filepath.Ext(basename))
		if oldlen <= len(basename) || !recursive {
			break
		}
		oldlen = len(basename)
	}
	return
}

func extractTagsFromPath(path string) (tags []string) {
	basename := toBasenameWithoutExt(path, true)
	tags = strings.Split(basename, "_")[1:]
	return
}

type DotSource struct {
	Name string
	Path string
	Tags []string
}

func searchMatchFile(baseDir string, tagMap map[string]string, ruleConf RuleConf) (sourceMap map[string]DotSource) {
	sourceMap = make(map[string]DotSource)
	filepath.Walk(
		baseDir,
		func(path string, info os.FileInfo, err error) (newerr error) {
			if err != nil || info.IsDir() {
				return
			}
			name := strings.TrimPrefix(path, baseDir)
			name = strings.TrimPrefix(name, "/")
			if ruleConf.Pattern.MatchString(name) {
				tags := extractTagsFromPath(name)
				for _, tag := range tags {
					if _, ok := tagMap[tag]; !ok {
						return
					}
				}
				sourceMap[name] = DotSource{
					Name: name,
					Path: path,
					Tags: tags,
				}
			}
			return
		})
	return
}

// https://www.dotnetperls.com/duplicates-go
func removeDuplicates(elements []DotSource) []DotSource {
	// Use map to record duplicates as we find them.
	encountered := map[string]bool{}
	result := []DotSource{}

	for i := range elements {
		if encountered[elements[i].Path] == true {
			// Do not add duplicate.
		} else {
			// Record this element as an encountered element.
			encountered[elements[i].Path] = true
			// Append to result slice.
			result = append(result, elements[i])
		}
	}
	// Return the new slice.
	return result
}

func mergeSourceArrayMap(sourceArrayMap map[string][]DotSource) (sources []DotSource) {
	var names []string
	for name := range sourceArrayMap {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sourceArray := sourceArrayMap[name]
		sources = append(sources, sourceArray...)
	}
	sources = removeDuplicates(sources)
	return
}

// http://stackoverflow.com/questions/15323767/does-golang-have-if-x-in-construct-similar-to-python
func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func appendDotGtp(outFile *os.File, source DotSource, tagMap map[string]string) (err error) {
	tpl, err := template.ParseFiles(source.Path)
	if err != nil {
		return
	}

	err = tpl.Execute(outFile, tagMap)
	if err != nil {
		return
	}
	return
}

func appendDotText(outFile *os.File, source DotSource, tagMap map[string]string) (err error) {
	inFile, err := os.Open(source.Path)
	defer inFile.Close()
	if err != nil {
		return
	}
	_, err = io.Copy(outFile, inFile)
	if err != nil {
		return
	}
	return
}

func appendDot(outFile *os.File, source DotSource, tagMap map[string]string) (err error) {
	if stringInSlice("gtp", source.Tags) {
		err = appendDotGtp(outFile, source, tagMap)
	} else {
		err = appendDotText(outFile, source, tagMap)
	}
	return
}

func CatDots(outFilePath string, sources []DotSource, tagMap map[string]string) (err error) {
	// expand ~/
	usr, _ := user.Current()
	homedir := usr.HomeDir
	if strings.HasPrefix(outFilePath, "~/") {
		outFilePath = strings.Replace(outFilePath, "~/", homedir, 1)
	}

	// mkdir -p
	dir := filepath.Dir(outFilePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// use FileMode of current dir
		fi, err := os.Stat(filepath.Dir(os.Args[0]))
		if err != nil {
			return err
		}
		os.MkdirAll(dir, fi.Mode())
	}

	outFile, err := os.Create(outFilePath)
	defer outFile.Close()
	if err != nil {
		return
	}

	for _, source := range sources {
		fmt.Println("- " + source.Path)
		err = appendDot(outFile, source, tagMap)
		if err != nil {
			return
		}
	}
	return
}

func Polkadot(polkaDirPaths []string, tagMap map[string]string, ruleConfMap map[string]RuleConf) (err error) {
	for outFile, ruleConf := range ruleConfMap {
		sourceArrayMap := make(map[string][]DotSource)
		color.New(color.FgBlue).Add(color.Bold).Println(outFile)
		for _, dir := range ruleConf.Directories {
			for _, rootDir := range polkaDirPaths {
				baseDir := rootDir + dir
				sourceMap := searchMatchFile(baseDir, tagMap, ruleConf)
				for name, source := range sourceMap {
					_, ok := sourceArrayMap[name]
					if !ok {
						sourceArrayMap[name] = make([]DotSource, 0)
					}
					sourceArrayMap[name] = append(sourceArrayMap[name], source)
				}
			}
		}
		sources := mergeSourceArrayMap(sourceArrayMap)
		err = CatDots(outFile, sources, tagMap)
		if err != nil {
			return
		}
	}
	return
}

const version = "0.0.2a"

func main() {
	versionFlag := flag.Bool("V", false, "show version info")
	flag.Parse()
	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}

	currentDirPath, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	entryTagsPath := flag.Arg(0)
	polkaDirPaths := flag.Args()[1:]

	entryTags := readEntryTags(entryTagsPath)
	tagMap := readPathConfs(polkaDirPaths)
	tagConf := readTagConfs(polkaDirPaths)
	ruleConf := readRuleConfs(polkaDirPaths)

	fmt.Println("dotfiles dir: " + currentDirPath)
	fmt.Println("entry tags path: " + entryTagsPath)
	fmt.Printf("entry tags: %v\n", entryTags)
	fmt.Printf("paths: %v\n", tagMap)

	tagMap["dotfiles"] = currentDirPath
	tagMap["gtp"] = "gtp"
	entryTags["default"] = "default"

	acceptTags, rejectTags := resolveTags(tagConf, entryTags)
	for tag, value := range acceptTags {
		tagMap[tag] = value
	}
	for tag, _ := range rejectTags {
		delete(tagMap, tag)
	}

	fmt.Printf("resolved tags: %v\n", tagMap)
	fmt.Printf("rules: %v\n", ruleConf)

	err = Polkadot(polkaDirPaths, tagMap, ruleConf)
	if err != nil {
		panic(err)
	}
}
