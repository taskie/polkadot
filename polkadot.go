package main

import (
	"cmp"
	"container/list"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/fatih/color"
	"gopkg.in/yaml.v2"
)

const version = "0.1.0"

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
	dotEntries []DotEntry
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

	acceptedTags, rejectedTags, err := a.Expand()
	if err != nil {
		return err
	}
	log.Printf("accepted tags: %+v\n", acceptedTags)
	log.Printf("rejected tags: %+v\n", rejectedTags)

	tagMap, err := a.Collect()
	if err != nil {
		return err
	}
	log.Printf("collected tags: %+v\n", tagMap)
	tagMap["dotfiles"] = a.dotfilesDirPath
	tagMap["gtp"] = "gtp"
	for tag, value := range acceptedTags {
		tagMap[tag] = value
	}
	for tag := range rejectedTags {
		delete(tagMap, tag)
	}
	log.Printf("resolved tags: %+v\n", tagMap)
	a.tagMap = tagMap

	dotEntries, err := a.Weave()
	if err != nil {
		return err
	}
	log.Printf("sources: (following)")
	for _, entry := range dotEntries {
		if entry.Target.Mode != nil {
			color.New(color.FgBlue).Printf("%s (mode: %o)\n", entry.Path(), *entry.Target.Mode)
		} else {
			color.New(color.FgBlue).Println(entry.Path())
		}
		for _, source := range entry.Sources {
			fmt.Println("- " + source.Path)
		}
	}
	a.dotEntries = dotEntries
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
	buf, err := os.ReadFile(a.entryPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.entryPath, err)
	}
	var props map[string]string
	err = yaml.Unmarshal(buf, &props)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", a.entryPath, err)
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
		confPath := filepath.Join(dirPath, "paths.yml")
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := os.ReadFile(confPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", confPath, err)
		}
		var pathsConf PathsConf
		err = yaml.Unmarshal(buf, &pathsConf)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", confPath, err)
		}
		subProps, err := collector.Collect(pathsConf)
		if err != nil {
			return nil, fmt.Errorf("collect %s: %w", confPath, err)
		}
		for key, value := range subProps {
			props[key] = value
		}
	}
	return props, nil
}

func (a *App) LoadTags() (map[string]map[string]string, error) {
	propsDef := make(map[string]map[string]string)
	for _, dirPath := range a.polkaDirPaths {
		confPath := filepath.Join(dirPath, "tags.yml")
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := os.ReadFile(confPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", confPath, err)
		}
		var tagConfMap map[string]map[string]string
		err = yaml.Unmarshal(buf, &tagConfMap)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", confPath, err)
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
		confPath := filepath.Join(dirPath, "rules.yml")
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := os.ReadFile(confPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", confPath, err)
		}
		var rulesConf RulesConf
		err = yaml.Unmarshal(buf, &rulesConf)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", confPath, err)
		}
		for k, v := range rulesConf {
			if v.Dir != "" {
				v.Dirs = append(v.Dirs, v.Dir)
			}
			var mode *int = nil
			if v.Mode != "" {
				modeInt, err := strconv.ParseInt(v.Mode, 8, 32)
				if err != nil {
					return nil, fmt.Errorf("%s: rule %q: invalid mode %q: %w", confPath, k, v.Mode, err)
				}
				if modeInt < 0 || modeInt > 0777 {
					return nil, fmt.Errorf("invalid mode: %s", v.Mode)
				}
				modeValue := int(modeInt)
				mode = &modeValue
			}
			pat, err := regexp.Compile(v.Pat)
			if err != nil {
				return nil, fmt.Errorf("rules.yml: rule %q: invalid pattern %q: %w", k, v.Pat, err)
			}
			ruleConfMap[k] = WeaverRule{
				Directories: v.Dirs,
				Pattern:     pat,
				Mode:        mode,
			}
		}
	}
	return ruleConfMap, nil
}

func (a *App) Weave() ([]DotEntry, error) {
	weaver := Weaver{}
	return weaver.Weave(a.polkaDirPaths, a.tagMap, a.ruleConfMap)
}

func (a *App) Expand() (map[string]string, map[string]string, error) {
	expander := Expander{}
	acceptedTags, rejectedTags := expander.Expand(a.tagConf, a.entryTags)
	return acceptedTags, rejectedTags, nil
}

func (a *App) Generate() error {
	generator := Generator{}
	for _, entry := range a.dotEntries {
		if err := generator.Generate(entry, a.tagMap); err != nil {
			return fmt.Errorf("generate %s: %w", entry.Path(), err)
		}
	}
	return nil
}

// Collect

type PathsConf map[string][]CollectorEntry

type Collector struct{}

type CollectorEntry struct {
	Type string
	Name string
	Path string
}

func (c *Collector) Collect(pathsConf PathsConf) (map[string]string, error) {
	props := make(map[string]string)
	for key, entries := range pathsConf {
		for _, entry := range entries {
			name := key
			if entry.Name != "" {
				name = entry.Name
			}
			if entry.Type == "exec" {
				if fullPath, err := exec.LookPath(name); err == nil {
					props[key] = fullPath
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
							props[key] = fullPath
						}
					}
				}
			} else if entry.Type == "env" {
				env := os.Getenv(name)
				if env != "" {
					props[key] = env
				}
			} else {
				return nil, fmt.Errorf("unknown env collector entry type: %s", entry.Type)
			}
		}
	}
	return props, nil
}

// Expand

type Expander struct{}

type tagItem struct {
	Tag        string
	Value      string
	Depth      int
	Negative   bool
	Importance int
}

func makeTagItem(rawTag string, value string, depth int) tagItem {
	negative := false
	importance := 0
	tag := rawTag
	for strings.HasPrefix(tag, "!") {
		exclamationCount := 0
		for _, c := range tag {
			if c == '!' {
				exclamationCount++
			} else {
				break
			}
		}
		negative = exclamationCount%2 == 1
		tag = rawTag[exclamationCount:]
		importance = exclamationCount
	}
	return tagItem{
		Tag:        tag,
		Value:      value,
		Depth:      depth,
		Negative:   negative,
		Importance: importance,
	}
}

func (e *Expander) walk(tagConf map[string]map[string]string, entryTags map[string]string) []tagItem {
	queue := list.New()
	for k, v := range entryTags {
		queue.PushBack(makeTagItem(k, v, 0))
	}
	seenTags := make(map[string]int)
	tagItems := make([]tagItem, 0)

	// breadth first search
	for queue.Len() > 0 {
		item := queue.Remove(queue.Front()).(tagItem)
		tagItems = append(tagItems, item)

		if depth, ok := seenTags[item.Tag]; ok {
			if depth < item.Depth {
				continue
			}
		}
		seenTags[item.Tag] = item.Depth

		if item.Negative {
			continue
		}

		newTags := tagConf[item.Tag]
		for newTag, v := range newTags {
			item := makeTagItem(newTag, v, item.Depth+1)
			queue.PushBack(item)
		}
	}

	// order by importance desc, depth (, tag, value)
	slices.SortFunc(tagItems, func(a, b tagItem) int {
		importance := cmp.Compare(b.Importance, a.Importance)
		if importance != 0 {
			return importance
		}
		depth := cmp.Compare(a.Depth, b.Depth)
		if depth != 0 {
			return depth
		}
		tag := cmp.Compare(a.Tag, b.Tag)
		if tag != 0 {
			return tag
		}
		return cmp.Compare(a.Value, b.Value)
	})

	// dedup
	uniqTagItems := make([]tagItem, 0)
	uniqTags := make(map[string]struct{})
	for _, item := range tagItems {
		if _, ok := uniqTags[item.Tag]; ok {
			continue
		}
		uniqTags[item.Tag] = struct{}{}
		uniqTagItems = append(uniqTagItems, item)
	}

	return uniqTagItems
}

func (e *Expander) Expand(tagConf map[string]map[string]string, entryTags map[string]string) (acceptedTags map[string]string, rejectedTags map[string]string) {
	tagItems := e.walk(tagConf, entryTags)

	acceptedTags = make(map[string]string)
	rejectedTags = make(map[string]string)

	for _, item := range tagItems {
		if item.Negative {
			rejectedTags[item.Tag] = item.Value
		} else {
			acceptedTags[item.Tag] = item.Value
		}
	}

	return acceptedTags, rejectedTags
}

// Weave

type RulesConf map[string]WeaverEntry

type Weaver struct{}

type WeaverEntry struct {
	Dir  string
	Dirs []string
	Pat  string
	Mode string
}

type WeaverRule struct {
	Directories []string
	Pattern     *regexp.Regexp
	Mode        *int
}

type DotSource struct {
	Name string
	Path string
	Tags []string
}

type DotTarget struct {
	Path string
	Mode *int
}

type DotEntry struct {
	Sources []DotSource
	Target  DotTarget
}

func (e *DotEntry) Path() string {
	return e.Target.Path
}

func (w *Weaver) Weave(polkaDirPaths []string, tagMap map[string]string, ruleConfMap map[string]WeaverRule) ([]DotEntry, error) {
	sourcesMap := make(map[string][]DotSource)
	targetMap := make(map[string]DotTarget)
	for outFile, ruleConf := range ruleConfMap {
		sourceArrayMap := make(map[string][]DotSource)
		for _, dir := range ruleConf.Directories {
			for _, rootDir := range polkaDirPaths {
				baseDir := filepath.Join(rootDir, dir)
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
		targetMap[outFile] = DotTarget{
			Path: outFile,
			Mode: ruleConf.Mode,
		}
	}
	dotEntries := dotMapsToEntries(sourcesMap, targetMap)
	return dotEntries, nil
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
		return nil, fmt.Errorf("walk %s: %w", baseDir, err)
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
	slices.Sort(names)

	for _, name := range names {
		sourceArray := sourceArrayMap[name]
		sources = append(sources, sourceArray...)
	}
	sources = removeDuplicatedDotSource(sources)
	return
}

// Stabilizes the order of dot entries.
func dotMapsToEntries(sourcesMap map[string][]DotSource, targetMap map[string]DotTarget) []DotEntry {
	entries := make([]DotEntry, 0, len(sourcesMap))
	for outFilePath, sources := range sourcesMap {
		target := targetMap[outFilePath]
		entry := DotEntry{Sources: sources, Target: target}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b DotEntry) int {
		return cmp.Compare(a.Path(), b.Path())
	})
	return entries
}

// Generate

type Generator struct{}

func (g *Generator) appendDotGtp(w io.Writer, source DotSource, tagMap map[string]string) error {
	tpl, err := template.ParseFiles(source.Path)
	if err != nil {
		return fmt.Errorf("parse template %s: %w", source.Path, err)
	}
	tpl = tpl.Option("missingkey=zero")
	if err := tpl.Execute(w, tagMap); err != nil {
		return fmt.Errorf("execute template %s: %w", source.Path, err)
	}
	return nil
}

func (g *Generator) appendDotText(w io.Writer, source DotSource, tagMap map[string]string) error {
	inFile, err := os.Open(source.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", source.Path, err)
	}
	defer inFile.Close()
	if _, err = io.Copy(w, inFile); err != nil {
		return fmt.Errorf("copy %s: %w", source.Path, err)
	}
	return nil
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

func (g *Generator) Generate(dotEntry DotEntry, tagMap map[string]string) error {
	// expand ~/
	outFilePath := expandHome(dotEntry.Path())

	// mkdir -p
	dir := filepath.Dir(outFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	mode := 0644
	if dotEntry.Target.Mode != nil {
		mode = *dotEntry.Target.Mode
	}
	outFile, err := os.OpenFile(outFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(mode))
	if err != nil {
		return fmt.Errorf("create %s: %w", outFilePath, err)
	}
	defer outFile.Close()

	return g.concatDots(outFile, dotEntry.Sources, tagMap)
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
