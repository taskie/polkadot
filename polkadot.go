package main

import (
	"bytes"
	"cmp"
	"container/list"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/fatih/color"
	"go.yaml.in/yaml/v3"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = ""

// getVersion returns the injected version, falling back to the module version
// recorded by `go install ...@vX.Y.Z`, or "dev" for local builds.
func getVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

// Output policy: stdout carries the result (the list of target files, plus
// their sources with -v); stderr carries status headers and debug logs, which
// are shown only with -v, except for info messages, the dry-run notice and
// errors.

func main() {
	err := run()
	if err != nil {
		color.New(color.FgRed, color.Bold).Fprint(os.Stderr, "* Failed: ")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configFlag := flag.String("c", "", "path to "+configFileName+" (default: ./"+configFileName+" if it exists)")
	dryRunFlag := flag.Bool("n", false, "performs a trial run")
	rawFlag := flag.Bool("raw", false, "concatenate files without normalizing newlines")
	verboseFlag := flag.Bool("v", false, "shows status headers, debug logs, and sources")
	versionFlag := flag.Bool("V", false, "shows version info")
	flag.Parse()
	if *versionFlag {
		fmt.Println(getVersion())
		return nil
	}

	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	var raw *bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "raw" {
			raw = rawFlag
		}
	})
	app, err := NewApp(pwd, *configFlag, flag.Args(), raw)
	if err != nil {
		return err
	}
	app.verbose = *verboseFlag

	app.status(color.FgCyan, "Preparing...")
	err = app.Prepare()
	if err != nil {
		return err
	}
	if *dryRunFlag {
		color.New(color.FgYellow, color.Bold).Fprintln(os.Stderr, "* Dry-run mode is enabled.")
	} else {
		app.status(color.FgCyan, "Executing...")
		err = app.Execute()
		if err != nil {
			return err
		}
	}
	app.status(color.FgGreen, "Completed.")
	return nil
}

// Config

const configFileName = "polkadot.yml"

type Config struct {
	Entries    []EntrySpec       `yaml:"entries"`
	Tags       map[string]string `yaml:"tags"`
	Components []string          `yaml:"components"`
	Raw        *bool             `yaml:"raw"`
}

// EntrySpec is an entry file reference in polkadot.yml. It is written either
// as a plain path or as {path: ..., optional: true}; an optional entry file is
// skipped when it does not exist.
type EntrySpec struct {
	Path     string `yaml:"path"`
	Optional bool   `yaml:"optional"`
}

func (e *EntrySpec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*e = EntrySpec{Path: node.Value}
	case yaml.MappingNode:
		// node.Decode does not inherit KnownFields, so check keys here.
		for i := 0; i < len(node.Content); i += 2 {
			switch key := node.Content[i].Value; key {
			case "path", "optional":
			default:
				return fmt.Errorf("line %d: unknown entry field %q", node.Content[i].Line, key)
			}
		}
		type plain EntrySpec
		if err := node.Decode((*plain)(e)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("line %d: entry must be a path or {path, optional}", node.Line)
	}
	if e.Path == "" {
		return fmt.Errorf("line %d: entry path must not be empty", node.Line)
	}
	return nil
}

// LoadConfig reads a polkadot.yml. Relative paths in it are resolved against
// the directory containing the file. An absent `entries` defaults to entry.yml.
func LoadConfig(path string) (*Config, error) {
	var config Config
	if err := loadYAML(path, &config); err != nil {
		return nil, err
	}
	if config.Entries == nil {
		config.Entries = []EntrySpec{{Path: "entry.yml"}}
	}
	baseDir := filepath.Dir(path)
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(baseDir, p)
	}
	for i := range config.Entries {
		config.Entries[i].Path = resolve(config.Entries[i].Path)
	}
	for i := range config.Components {
		config.Components[i] = resolve(config.Components[i])
	}
	return &config, nil
}

// NewApp builds an App from CLI inputs. If configPath is empty, ./polkadot.yml
// is used when present; otherwise it falls back to ./entry.yml and args.
// Non-empty args replace the configured components; a non-nil raw overrides
// the configured value.
func NewApp(pwd string, configPath string, args []string, raw *bool) (*App, error) {
	if configPath == "" {
		defaultPath := filepath.Join(pwd, configFileName)
		if _, err := os.Stat(defaultPath); err == nil {
			configPath = defaultPath
		}
	}
	app := &App{
		dotfilesDirPath: pwd,
		entries:         []EntrySpec{{Path: filepath.Join(pwd, "entry.yml")}},
		polkaDirPaths:   args,
	}
	if configPath != "" {
		if !filepath.IsAbs(configPath) {
			configPath = filepath.Join(pwd, configPath)
		}
		config, err := LoadConfig(configPath)
		if err != nil {
			return nil, err
		}
		app.configPath = configPath
		app.dotfilesDirPath = filepath.Dir(configPath)
		app.entries = config.Entries
		app.inlineTags = config.Tags
		if len(args) == 0 {
			app.polkaDirPaths = config.Components
		}
		if config.Raw != nil {
			app.rawConcat = *config.Raw
		}
	}
	if raw != nil {
		app.rawConcat = *raw
	}
	return app, nil
}

// Application

type App struct {
	// Input
	configPath      string
	dotfilesDirPath string
	entries         []EntrySpec
	inlineTags      map[string]string
	polkaDirPaths   []string
	verbose         bool
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
	rawConcat bool
}

// logf writes a debug log to stderr only in verbose mode.
func (a *App) logf(format string, v ...any) {
	if a.verbose {
		log.Printf(format, v...)
	}
}

// infof writes an informational message to stderr regardless of verbosity.
func (a *App) infof(format string, v ...any) {
	color.New(color.FgCyan).Fprint(os.Stderr, "info: ")
	fmt.Fprintf(os.Stderr, format, v...)
}

// status writes a colored status header to stderr only in verbose mode.
func (a *App) status(attr color.Attribute, msg string) {
	if a.verbose {
		color.New(attr, color.Bold).Fprintln(os.Stderr, "* "+msg)
	}
}

func (a *App) Prepare() error {
	if a.configPath != "" {
		a.logf("config: %s\n", a.configPath)
	}
	a.logf("dotfiles dir: %s\n", a.dotfilesDirPath)
	a.logf("entry files: %+v\n", a.entries)
	a.logf("component dirs: %+v\n", a.polkaDirPaths)

	if err := a.CheckComponents(); err != nil {
		return err
	}

	entryTags, err := a.LoadEntry()
	if err != nil {
		return err
	}
	entryTags["default"] = "default"
	a.logf("entry tags: %+v\n", entryTags)
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
	a.logf("accepted tags: %+v\n", acceptedTags)
	a.logf("rejected tags: %+v\n", rejectedTags)

	tagMap, err := a.Collect()
	if err != nil {
		return err
	}
	a.logf("collected tags: %+v\n", tagMap)
	tagMap["dotfiles"] = a.dotfilesDirPath
	tagMap["gtp"] = "gtp"
	for tag, value := range acceptedTags {
		tagMap[tag] = value
	}
	for tag := range rejectedTags {
		delete(tagMap, tag)
	}
	a.logf("resolved tags: %+v\n", tagMap)
	a.tagMap = tagMap

	wovenEntries, err := a.Weave()
	if err != nil {
		return err
	}
	// A rule without sources is skipped so that it never empties an existing
	// file (e.g. a host-specific fragment that is gated off on this machine).
	dotEntries := make([]DotEntry, 0, len(wovenEntries))
	for _, entry := range wovenEntries {
		if len(entry.Sources) == 0 {
			a.infof("%s: no sources, skipped\n", entry.Path())
			continue
		}
		dotEntries = append(dotEntries, entry)
	}
	for _, entry := range dotEntries {
		if entry.Target.Mode != nil {
			color.New(color.FgBlue).Printf("%s (mode: %o)\n", entry.Path(), *entry.Target.Mode)
		} else {
			color.New(color.FgBlue).Println(entry.Path())
		}
		if a.verbose {
			for _, source := range entry.Sources {
				fmt.Println("- " + source.Path)
			}
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

// CheckComponents verifies that every component path is an existing directory,
// so a typo or a file passed by mistake fails with a clear message instead of
// being silently ignored.
func (a *App) CheckComponents() error {
	for _, dirPath := range a.polkaDirPaths {
		fi, err := os.Stat(dirPath)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("component dir %s: not found", dirPath)
		}
		if err != nil {
			return fmt.Errorf("component dir %s: %w", dirPath, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("component dir %s: not a directory", dirPath)
		}
	}
	return nil
}

func (a *App) LoadEntry() (map[string]string, error) {
	props := make(map[string]string)
	for _, entry := range a.entries {
		var subProps map[string]string
		err := loadYAML(entry.Path, &subProps)
		if entry.Optional && errors.Is(err, fs.ErrNotExist) {
			a.logf("skip optional entry file: %s\n", entry.Path)
			continue
		}
		if err != nil {
			return nil, err
		}
		for k, v := range subProps {
			props[k] = v // overwrite
		}
	}
	for k, v := range a.inlineTags {
		props[k] = v // overwrite
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
		var pathsConf PathsConf
		err := loadYAML(confPath, &pathsConf)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := pathsConf.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", confPath, err)
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
		var tagConfMap map[string]map[string]string
		err := loadYAML(confPath, &tagConfMap)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
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
		confPath := filepath.Join(dirPath, "rules.yml")
		var rulesConf RulesConf
		err := loadYAML(confPath, &rulesConf)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for k, v := range rulesConf {
			if v.Dir == "" && len(v.Dirs) == 0 {
				return nil, fmt.Errorf("%s: rule %q: dir or dirs is required", confPath, k)
			}
			if v.Pat == "" {
				return nil, fmt.Errorf("%s: rule %q: pat is required", confPath, k)
			}
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
					return nil, fmt.Errorf("%s: rule %q: mode %q out of range 0-777", confPath, k, v.Mode)
				}
				modeValue := int(modeInt)
				mode = &modeValue
			}
			pat, err := regexp.Compile(v.Pat)
			if err != nil {
				return nil, fmt.Errorf("%s: rule %q: invalid pattern %q: %w", confPath, k, v.Pat, err)
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
	generator := Generator{NormalizeJoin: !a.rawConcat}
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
	Type string `yaml:"type"`
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// Validate checks that every candidate has a known type and that file/dir
// candidates have a path.
func (pc PathsConf) Validate() error {
	for key, entries := range pc {
		for i, entry := range entries {
			switch entry.Type {
			case "exec", "env":
			case "file", "dir":
				if entry.Path == "" {
					return fmt.Errorf("tag %q: candidate %d: type %q requires path", key, i, entry.Type)
				}
			case "":
				return fmt.Errorf("tag %q: candidate %d: type is required", key, i)
			default:
				return fmt.Errorf("tag %q: candidate %d: unknown type %q", key, i, entry.Type)
			}
		}
	}
	return nil
}

// Collect resolves each tag from its candidates in order; the first candidate
// that resolves wins, and a tag with no resolving candidate is left unset.
func (c *Collector) Collect(pathsConf PathsConf) (map[string]string, error) {
	props := make(map[string]string)
	for key, entries := range pathsConf {
		for _, entry := range entries {
			value, ok, err := c.resolve(key, entry)
			if err != nil {
				return nil, fmt.Errorf("tag %q: %w", key, err)
			}
			if ok {
				props[key] = value
				break
			}
		}
	}
	return props, nil
}

func (c *Collector) resolve(key string, entry CollectorEntry) (string, bool, error) {
	name := key
	if entry.Name != "" {
		name = entry.Name
	}
	switch entry.Type {
	case "exec":
		if fullPath, err := exec.LookPath(name); err == nil {
			return fullPath, true, nil
		}
	case "file", "dir":
		filePath, err := expandHome(entry.Path)
		if err != nil {
			return "", false, err
		}
		if fi, err := os.Stat(filePath); err == nil && fi.IsDir() == (entry.Type == "dir") {
			if fullPath, err := filepath.Abs(filePath); err == nil {
				return fullPath, true, nil
			}
		}
	case "env":
		if env := os.Getenv(name); env != "" {
			return env, true, nil
		}
	default:
		return "", false, fmt.Errorf("unknown collector entry type: %s", entry.Type)
	}
	return "", false, nil
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
	if strings.HasPrefix(tag, "!") {
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
	Dir  string   `yaml:"dir"`
	Dirs []string `yaml:"dirs"`
	Pat  string   `yaml:"pat"`
	Mode string   `yaml:"mode"`
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
	err := filepath.WalkDir(
		baseDir,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// A component does not need to have every rule's directory.
				if path == baseDir && errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
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

var excessNewlines = regexp.MustCompile(`\n{3,}`)

type Generator struct {
	NormalizeJoin bool
}

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
	if slices.Contains(source.Tags, "gtp") {
		err = g.appendDotGtp(w, source, tagMap)
	} else {
		err = g.appendDotText(w, source, tagMap)
	}
	return err
}

func (g *Generator) concatDots(w io.Writer, sources []DotSource, tagMap map[string]string) error {
	for i, source := range sources {
		if !g.NormalizeJoin {
			if err := g.appendDot(w, source, tagMap); err != nil {
				return err
			}
			continue
		}
		var buf bytes.Buffer
		if err := g.appendDot(&buf, source, tagMap); err != nil {
			return err
		}
		content := bytes.TrimRight(buf.Bytes(), "\n")
		if _, err := w.Write(content); err != nil {
			return err
		}
		sep := "\n\n"
		if i == len(sources)-1 {
			sep = "\n"
		}
		if _, err := io.WriteString(w, sep); err != nil {
			return err
		}
	}
	return nil
}

func (g *Generator) Generate(dotEntry DotEntry, tagMap map[string]string) error {
	// expand ~/
	outFilePath, err := expandHome(dotEntry.Path())
	if err != nil {
		return err
	}

	// mkdir -p
	dir := filepath.Dir(outFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	mode := 0644
	if dotEntry.Target.Mode != nil {
		mode = *dotEntry.Target.Mode
	}
	var buf bytes.Buffer
	if err := g.concatDots(&buf, dotEntry.Sources, tagMap); err != nil {
		return err
	}
	content := buf.Bytes()
	if g.NormalizeJoin {
		content = excessNewlines.ReplaceAll(content, []byte("\n\n"))
	}

	return writeFileAtomic(outFilePath, content, os.FileMode(mode))
}

// writeFileAtomic writes content to a temporary file in the same directory and
// renames it over path, so a failure never leaves a partially written file.
// If path is a symlink, the file it points to is replaced and the link is kept.
// The mode is applied exactly (regardless of umask), also to existing files.
func writeFileAtomic(path string, content []byte, mode os.FileMode) (err error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve symlink %s: %w", path, err)
		}
		path = resolved
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".polkadot-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()
	if _, err = tmpFile.Write(content); err != nil {
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err = tmpFile.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err = tmpFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}
	return nil
}

// Utils

// loadYAML reads and strictly decodes the YAML file at path. A missing file is
// reported as an error wrapping fs.ErrNotExist, so optional files can be
// skipped with errors.Is while other read errors still fail.
func loadYAML(path string, v any) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := decodeYAML(buf, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// decodeYAML decodes a YAML document strictly: unknown struct fields and
// duplicate keys are errors. An empty document decodes to the zero value.
func decodeYAML(buf []byte, v any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(buf))
	decoder.KnownFields(true)
	if err := decoder.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand home: %w", err)
	}
	return filepath.Join(home, path[2:]), nil
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
