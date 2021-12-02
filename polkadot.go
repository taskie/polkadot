package main

import (
	"container/list"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/fatih/color"
	"gopkg.in/yaml.v2"
)

const version = "0.0.3"

func main() {
	err := run()
	if err != nil {
		color.New(color.FgRed, color.Bold).Println("* Failed.")
		log.Fatal(err)
	}
	color.New(color.FgGreen, color.Bold).Println("* Completed.")
}

func run() error {
	dryRunFlag := flag.Bool("n", false, "performs a trial run")
	versionFlag := flag.Bool("V", false, "shows version info")
	flag.Parse()
	if *versionFlag {
		fmt.Println(version)
		return nil
	}

	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	polkaDirPaths := flag.Args()

	app := App{
		dotfilesDirPath: pwd,
		entryPath:       "entry.yml",
		polkaDirPaths:   polkaDirPaths,
	}

	color.New(color.FgCyan, color.Bold).Println("* Preparing...")
	err = app.Prepare()
	if err != nil {
		return err
	}
	if *dryRunFlag {
		color.New(color.FgYellow, color.Bold).Println("* Dry-run mode is enabled.")
	} else {
		color.New(color.FgCyan, color.Bold).Println("* Executing...")
		err = app.Execute()
		if err != nil {
			return err
		}
	}
	return nil
}

// Application

type App struct {
	// Input
	dotfilesDirPath string
	entryPath       string
	polkaDirPaths   []string
	// Load
	entryTags   map[string]string
	tagConf     map[string]map[string]string
	ruleConfMap map[string]WeaverRule
	// Expand
	// Collect
	tagMap map[string]string
	// Weave
	sourcesMap     map[string][]DotSource
	sourcesEntries []DotSourcesEntry
	// Generate
}

func (a *App) Prepare() error {
	log.Printf("dotfiles dir: %s\n", a.dotfilesDirPath)
	log.Printf("component dirs: %+v\n", a.polkaDirPaths)

	entryTags, err := a.LoadEntry()
	if err != nil {
		return err
	}
	entryTags["default"] = "default"
	log.Printf("entry tags: %+v\n", entryTags)
	a.entryTags = entryTags

	tagConf, err := a.LoadTags()
	if err != nil {
		return err
	}
	a.tagConf = tagConf

	ruleConf, err := a.LoadRules()
	if err != nil {
		return err
	}
	a.ruleConfMap = ruleConf

	acceptTags, rejectTags, err := a.Expand()
	if err != nil {
		return err
	}
	log.Printf("accept tags: %+v\n", acceptTags)
	log.Printf("reject tags: %+v\n", rejectTags)

	tagMap, err := a.Collect()
	if err != nil {
		return err
	}
	log.Printf("collected tags: %+v\n", tagMap)
	tagMap["dotfiles"] = a.dotfilesDirPath
	tagMap["gtp"] = "gtp"
	for tag, value := range acceptTags {
		tagMap[tag] = value
	}
	for tag := range rejectTags {
		delete(tagMap, tag)
	}
	log.Printf("resolved tags: %+v\n", tagMap)
	a.tagMap = tagMap

	sourcesMap, err := a.Weave()
	if err != nil {
		return err
	}
	sourcesEntries := dotSourcesMapToEntries(sourcesMap)
	log.Printf("sources: (following)")
	for _, entry := range sourcesEntries {
		color.New(color.FgBlue).Println(entry.path)
		for _, source := range entry.sources {
			fmt.Println("- " + source.Path)
		}
	}
	a.sourcesMap = sourcesMap
	a.sourcesEntries = sourcesEntries
	return nil
}

func (a *App) Execute() error {
	err := a.Generate()
	if err != nil {
		return err
	}
	return nil
}

// Application tasks

func (a *App) LoadEntry() (map[string]string, error) {
	buf, err := ioutil.ReadFile(a.entryPath)
	if err != nil {
		return nil, err
	}
	var props map[string]string
	err = yaml.Unmarshal(buf, &props)
	if err != nil {
		return nil, err
	}
	for k, v := range props {
		if v == "" {
			props[k] = k
		}
	}
	return props, nil
}

func (a *App) Collect() (map[string]string, error) {
	collector := Collector{}
	props := make(map[string]string)
	for _, dirPath := range a.polkaDirPaths {
		confPath := dirPath + "/paths.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			return nil, err
		}
		var pathsConf PathsConf
		err = yaml.Unmarshal(buf, &pathsConf)
		if err != nil {
			return nil, err
		}
		subProps, err := collector.Collect(pathsConf)
		if err != nil {
			return nil, err
		}
		for name, value := range subProps {
			props[name] = value
		}
	}
	return props, nil
}

func (a *App) LoadTags() (map[string]map[string]string, error) {
	propsDef := make(map[string]map[string]string)
	for _, dirPath := range a.polkaDirPaths {
		confPath := dirPath + "/tags.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			return nil, err
		}
		var tagConfMap map[string]map[string]string
		err = yaml.Unmarshal(buf, &tagConfMap)
		if err != nil {
			return nil, err
		}
		for tag, children := range tagConfMap {
			for k, v := range children {
				if v == "" {
					children[k] = k
				}
			}
			propsDef[tag] = children // overwrite
		}
	}
	return propsDef, nil
}

func (a *App) LoadRules() (map[string]WeaverRule, error) {
	ruleConfMap := make(map[string]WeaverRule)
	for _, dirPath := range a.polkaDirPaths {
		confPath := dirPath + "/rules.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			return nil, err
		}
		var rulesConf RulesConf
		err = yaml.Unmarshal(buf, &rulesConf)
		if err != nil {
			return nil, err
		}
		for k, v := range rulesConf {
			if v.Dir != "" {
				v.Dirs = append(v.Dirs, v.Dir)
			}
			ruleConfMap[k] = WeaverRule{
				Directories: v.Dirs,
				Pattern:     regexp.MustCompile(v.Pat),
			}
		}
	}
	return ruleConfMap, nil
}

func (a *App) Weave() (map[string][]DotSource, error) {
	weaver := Weaver{}
	return weaver.Weave(a.polkaDirPaths, a.tagMap, a.ruleConfMap)
}

func (a *App) Expand() (map[string]string, map[string]string, error) {
	expander := Expander{}
	acceptTags, rejectTags := expander.Expand(a.tagConf, a.entryTags)
	return acceptTags, rejectTags, nil
}

func (a *App) Generate() error {
	generator := Generator{}
	for _, entry := range a.sourcesEntries {
		err := generator.Generate(entry.path, entry.sources, a.tagMap)
		if err != nil {
			return err
		}
	}
	return nil
}

// Collect

type PathsConf map[string]*CollectorEntry

type Collector struct{}

type CollectorEntry struct {
	Type string
	Path string
}

func (c *Collector) Collect(pathsConf PathsConf) (map[string]string, error) {
	props := make(map[string]string)
	for name, entry := range pathsConf {
		if entry.Type == "exec" {
			if fullPath, err := exec.LookPath(name); err == nil {
				props[name] = fullPath
			}
		} else if entry.Type == "file" || entry.Type == "dir" {
			filePath := expandHome(entry.Path)
			if ft, err := os.Stat(filePath); err == nil {
				valid := true
				if entry.Type == "file" {
					valid = valid && !ft.IsDir()
				}
				if entry.Type == "dir" {
					valid = valid && ft.IsDir()
				}
				if valid {
					if fullPath, err := filepath.Abs(filePath); err == nil {
						props[name] = fullPath
					}
				}
			}
		} else if entry.Type == "env" {
			env := os.Getenv(entry.Path)
			props[name] = env
		} else {
			return nil, fmt.Errorf("unknown env collector entry type: %s", entry.Type)
		}
	}
	return props, nil
}

// Expand

type Expander struct{}

func (e *Expander) Expand(tagConf map[string]map[string]string, entryTags map[string]string) (acceptTags map[string]string, rejectTags map[string]string) {
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

// Weave

type RulesConf map[string]WeaverEntry

type Weaver struct{}

type WeaverEntry struct {
	Dir  string
	Dirs []string
	Pat  string
}

type WeaverRule struct {
	Directories []string
	Pattern     *regexp.Regexp
}

type DotSource struct {
	Name string
	Path string
	Tags []string
}

type DotSourcesEntry struct {
	path    string
	sources []DotSource
}

func (w *Weaver) Weave(polkaDirPaths []string, tagMap map[string]string, ruleConfMap map[string]WeaverRule) (map[string][]DotSource, error) {
	sourcesMap := make(map[string][]DotSource)
	for outFile, ruleConf := range ruleConfMap {
		sourceArrayMap := make(map[string][]DotSource)
		for _, dir := range ruleConf.Directories {
			for _, rootDir := range polkaDirPaths {
				baseDir := rootDir + dir
				sourceMap, err := w.Walk(baseDir, tagMap, ruleConf)
				if err != nil {
					return nil, err
				}
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
		sourcesMap[outFile] = sources
	}
	return sourcesMap, nil
}

func (w *Weaver) Walk(baseDir string, tagMap map[string]string, ruleConf WeaverRule) (map[string]DotSource, error) {
	sourceMap := make(map[string]DotSource)
	err := filepath.Walk(
		baseDir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			name := strings.TrimPrefix(path, baseDir)
			name = strings.TrimPrefix(name, "/")
			if ruleConf.Pattern.MatchString(name) {
				tags := extractTagsFromPath(name)
				for _, tag := range tags {
					if _, ok := tagMap[tag]; !ok {
						return nil
					}
				}
				sourceMap[name] = DotSource{
					Name: name,
					Path: path,
					Tags: tags,
				}
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return sourceMap, nil
}

func removeDuplicatedDotSource(sources []DotSource) []DotSource {
	set := make(map[string]struct{})
	list := make([]DotSource, 0)
	for _, source := range sources {
		if _, ok := set[source.Path]; !ok {
			set[source.Path] = struct{}{}
			list = append(list, source)
		}
	}
	return list
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
	sources = removeDuplicatedDotSource(sources)
	return
}

// Stabilizes the order of dot sources.
func dotSourcesMapToEntries(sourcesMap map[string][]DotSource) []DotSourcesEntry {
	entries := make([]DotSourcesEntry, 0, len(sourcesMap))
	for outFilePath, sources := range sourcesMap {
		entries = append(entries, DotSourcesEntry{path: outFilePath, sources: sources})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})
	return entries
}

// Generate

type Generator struct{}

func (g *Generator) appendDotGtp(w io.Writer, source DotSource, tagMap map[string]string) error {
	tpl, err := template.ParseFiles(source.Path)
	if err != nil {
		return err
	}
	err = tpl.Execute(w, tagMap)
	if err != nil {
		return err
	}
	return err
}

func (g *Generator) appendDotText(w io.Writer, source DotSource, tagMap map[string]string) (err error) {
	inFile, err := os.Open(source.Path)
	if err != nil {
		return
	}
	defer inFile.Close()
	_, err = io.Copy(w, inFile)
	if err != nil {
		return
	}
	return
}

func (g *Generator) appendDot(w io.Writer, source DotSource, tagMap map[string]string) error {
	var err error = nil
	if stringInSlice("gtp", source.Tags) {
		err = g.appendDotGtp(w, source, tagMap)
	} else {
		err = g.appendDotText(w, source, tagMap)
	}
	return err
}

func (g *Generator) concatDots(w io.Writer, sources []DotSource, tagMap map[string]string) error {
	var err error = nil
	for _, source := range sources {
		err = g.appendDot(w, source, tagMap)
		if err != nil {
			return err
		}
	}
	return err
}

func (g *Generator) Generate(outFilePath string, sources []DotSource, tagMap map[string]string) error {
	// expand ~/
	outFilePath = expandHome(outFilePath)

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
	if err != nil {
		return err
	}
	defer outFile.Close()

	return g.concatDots(outFile, sources, tagMap)
}

// Utils

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	usr, _ := user.Current()
	homedir := usr.HomeDir + "/"
	return strings.Replace(path, "~/", homedir, 1)
}

func toBasenameWithoutExt(path string, recursive bool) (basename string) {
	basename = filepath.Base(path)
	oldlen := len(basename)
	for {
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
