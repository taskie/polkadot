package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

func TestExpander(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		// GIVEN:
		e := Expander{}
		tagConf := map[string]map[string]string{
			"linux": {
				"systemctl": "/usr/bin/systemctl",
			},
			"arch": {
				"pacman": "pacman",
			},
			"emacs": {
				"emacsd": "emacsd",
			},
		}
		entryTags := map[string]string{
			"linux":  "linux",
			"arch":   "arch",
			"pacman": "/usr/bin/pacman",
			"emacs":  "/usr/bin/emacs",
		}
		// WHEN:
		acceptTags, rejectTags := e.Expand(tagConf, entryTags)
		// THEN:
		expectedAcceptTags := map[string]string{
			"linux":     "linux",
			"systemctl": "/usr/bin/systemctl",
			"arch":      "arch",
			"pacman":    "/usr/bin/pacman",
			"emacs":     "/usr/bin/emacs",
			"emacsd":    "emacsd",
		}
		expectedRejectTags := map[string]string{}
		if !reflect.DeepEqual(acceptTags, expectedAcceptTags) {
			t.Errorf("acceptTags: got %v, want %v", acceptTags, expectedAcceptTags)
		}
		if !reflect.DeepEqual(rejectTags, expectedRejectTags) {
			t.Errorf("rejectTags: got %v, want %v", rejectTags, expectedRejectTags)
		}
	})
	t.Run("hasRejectedTags", func(t *testing.T) {
		// GIVEN:
		e := Expander{}
		tagConf := map[string]map[string]string{
			"linux": {
				"systemctl": "/usr/bin/systemctl",
			},
			"arch": {
				"pacman": "pacman",
			},
			"emacs": {
				"emacsd": "emacsd",
			},
		}
		entryTags := map[string]string{
			"linux":      "linux",
			"arch":       "arch",
			"pacman":     "/usr/bin/pacman",
			"emacs":      "/usr/bin/emacs",
			"!emacsd":    "!emacsd",
			"!systemctl": "!systemctl",
		}
		// WHEN:
		acceptTags, rejectTags := e.Expand(tagConf, entryTags)
		// THEN:
		expectedAcceptTags := map[string]string{
			"arch":   "arch",
			"emacs":  "/usr/bin/emacs",
			"linux":  "linux",
			"pacman": "/usr/bin/pacman",
		}
		expectedRejectTags := map[string]string{
			"emacsd":    "!emacsd",
			"systemctl": "!systemctl",
		}
		if !reflect.DeepEqual(acceptTags, expectedAcceptTags) {
			t.Errorf("acceptTags: got %v, want %v", acceptTags, expectedAcceptTags)
		}
		if !reflect.DeepEqual(rejectTags, expectedRejectTags) {
			t.Errorf("rejectTags: got %v, want %v", rejectTags, expectedRejectTags)
		}
	})
	t.Run("hasDoubleNegative", func(t *testing.T) {
		// GIVEN:
		e := Expander{}
		tagConf := map[string]map[string]string{
			"linux": {
				"systemctl": "/usr/bin/systemctl",
			},
			"arch": {
				"pacman":      "pacman",
				"!!systemctl": "/usr/bin/systemctl",
			},
			"emacs": {
				"emacsd": "emacsd",
			},
		}
		entryTags := map[string]string{
			"linux":      "linux",
			"arch":       "arch",
			"pacman":     "/usr/bin/pacman",
			"!systemctl": "!systemctl",
			"emacs":      "/usr/bin/emacs",
			"!emacsd":    "!emacsd",
		}
		// WHEN:
		acceptTags, rejectTags := e.Expand(tagConf, entryTags)
		// THEN:
		expectedAcceptTags := map[string]string{
			"arch":      "arch",
			"emacs":     "/usr/bin/emacs",
			"linux":     "linux",
			"pacman":    "/usr/bin/pacman",
			"systemctl": "/usr/bin/systemctl",
		}
		expectedRejectTags := map[string]string{
			"emacsd": "!emacsd",
		}
		if !reflect.DeepEqual(acceptTags, expectedAcceptTags) {
			t.Errorf("acceptTags: got %v, want %v", acceptTags, expectedAcceptTags)
		}
		if !reflect.DeepEqual(rejectTags, expectedRejectTags) {
			t.Errorf("rejectTags: got %v, want %v", rejectTags, expectedRejectTags)
		}
	})
}

func TestCollector(t *testing.T) {
	c := Collector{}

	t.Run("env/set", func(t *testing.T) {
		t.Setenv("POLKADOT_TEST_VAR", "/test/path")
		props, err := c.Collect(PathsConf{
			"mykey": []CollectorEntry{{Type: "env", Name: "POLKADOT_TEST_VAR"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := props["mykey"]; got != "/test/path" {
			t.Errorf("got %q, want %q", got, "/test/path")
		}
	})

	t.Run("env/unset", func(t *testing.T) {
		props, err := c.Collect(PathsConf{
			"mykey": []CollectorEntry{{Type: "env", Name: "POLKADOT_TEST_NEVER_SET_XYZABC"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := props["mykey"]; ok {
			t.Error("expected key to be absent for unset env var")
		}
	})

	t.Run("file/exists", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "file.txt")
		os.WriteFile(p, nil, 0644)
		props, err := c.Collect(PathsConf{
			"myfile": []CollectorEntry{{Type: "file", Path: p}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if props["myfile"] == "" {
			t.Error("expected myfile to be set")
		}
	})

	t.Run("file/missing", func(t *testing.T) {
		props, err := c.Collect(PathsConf{
			"myfile": []CollectorEntry{{Type: "file", Path: "/nonexistent/path/file.txt"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := props["myfile"]; ok {
			t.Error("expected key to be absent for missing file")
		}
	})

	t.Run("file/rejects_dir", func(t *testing.T) {
		props, err := c.Collect(PathsConf{
			"myfile": []CollectorEntry{{Type: "file", Path: t.TempDir()}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := props["myfile"]; ok {
			t.Error("expected key to be absent when path is a directory")
		}
	})

	t.Run("dir/exists", func(t *testing.T) {
		props, err := c.Collect(PathsConf{
			"mydir": []CollectorEntry{{Type: "dir", Path: t.TempDir()}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if props["mydir"] == "" {
			t.Error("expected mydir to be set")
		}
	})

	t.Run("dir/rejects_file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "file.txt")
		os.WriteFile(p, nil, 0644)
		props, err := c.Collect(PathsConf{
			"mydir": []CollectorEntry{{Type: "dir", Path: p}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := props["mydir"]; ok {
			t.Error("expected key to be absent when path is a file")
		}
	})

	t.Run("exec/found", func(t *testing.T) {
		props, err := c.Collect(PathsConf{
			"sh": []CollectorEntry{{Type: "exec"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if props["sh"] == "" {
			t.Skip("sh not found in PATH")
		}
		if !filepath.IsAbs(props["sh"]) {
			t.Errorf("expected absolute path, got %q", props["sh"])
		}
	})

	t.Run("exec/missing", func(t *testing.T) {
		props, err := c.Collect(PathsConf{
			"nonexistent_binary_xyzabc": []CollectorEntry{{Type: "exec"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := props["nonexistent_binary_xyzabc"]; ok {
			t.Error("expected key to be absent for missing binary")
		}
	})
}

func TestWeaver(t *testing.T) {
	anyPat := regexp.MustCompile(`.*`)
	w := Weaver{}

	t.Run("tag_gating/accepts_matching", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config_linux.conf"), []byte("content"), 0644)

		sourceMap, err := w.Walk(dir, map[string]string{"linux": "linux"}, WeaverRule{Pattern: anyPat})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sourceMap["config_linux.conf"]; !ok {
			t.Error("expected config_linux.conf to be included")
		}
	})

	t.Run("tag_gating/rejects_missing_tag", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config_linux.conf"), []byte("content"), 0644)

		sourceMap, err := w.Walk(dir, map[string]string{}, WeaverRule{Pattern: anyPat})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sourceMap["config_linux.conf"]; ok {
			t.Error("expected config_linux.conf to be excluded")
		}
	})

	t.Run("tag_gating/requires_all_tags", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config_linux_arch.conf"), []byte("content"), 0644)

		sourceMap, err := w.Walk(dir, map[string]string{"linux": "linux"}, WeaverRule{Pattern: anyPat})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sourceMap["config_linux_arch.conf"]; ok {
			t.Error("expected config_linux_arch.conf to be excluded when arch tag is missing")
		}
	})

	t.Run("pattern_filtering", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "match.conf"), []byte("content"), 0644)
		os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("content"), 0644)

		pat := regexp.MustCompile(`\.conf$`)
		sourceMap, err := w.Walk(dir, map[string]string{}, WeaverRule{Pattern: pat})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sourceMap["match.conf"]; !ok {
			t.Error("expected match.conf to be included")
		}
		if _, ok := sourceMap["skip.txt"]; ok {
			t.Error("expected skip.txt to be excluded by pattern")
		}
	})

	t.Run("sort_stability", func(t *testing.T) {
		root := t.TempDir()
		dotsDir := filepath.Join(root, "dots")
		os.MkdirAll(dotsDir, 0755)
		os.WriteFile(filepath.Join(dotsDir, "b_source.conf"), []byte("b"), 0644)
		os.WriteFile(filepath.Join(dotsDir, "a_source.conf"), []byte("a"), 0644)

		ruleConfMap := map[string]WeaverRule{
			"/tmp/b": {Directories: []string{"dots"}, Pattern: regexp.MustCompile(`b_source`)},
			"/tmp/a": {Directories: []string{"dots"}, Pattern: regexp.MustCompile(`a_source`)},
		}
		entries, err := w.Weave([]string{root}, map[string]string{}, ruleConfMap)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0].Path() != "/tmp/a" || entries[1].Path() != "/tmp/b" {
			t.Errorf("unexpected order: %q, %q", entries[0].Path(), entries[1].Path())
		}
	})
}

func TestGenerator(t *testing.T) {
	g := Generator{NormalizeJoin: true}

	t.Run("text/concatenation", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "a.conf")
		p2 := filepath.Join(dir, "b.conf")
		os.WriteFile(p1, []byte("aaa\n"), 0644)
		os.WriteFile(p2, []byte("bbb\n"), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{
				{Name: "a.conf", Path: p1, Tags: []string{}},
				{Name: "b.conf", Path: p2, Tags: []string{}},
			},
			Target: DotTarget{Path: out},
		}
		if err := g.Generate(entry, nil); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "aaa\n\nbbb\n" {
			t.Errorf("got %q, want %q", string(content), "aaa\n\nbbb\n")
		}
	})

	t.Run("gtp/template_rendering", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config_gtp.conf")
		os.WriteFile(p, []byte(`home={{.home}}`), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{
				{Name: "config_gtp.conf", Path: p, Tags: []string{"gtp"}},
			},
			Target: DotTarget{Path: out},
		}
		if err := g.Generate(entry, map[string]string{"home": "/home/user"}); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "home=/home/user\n" {
			t.Errorf("got %q, want %q", string(content), "home=/home/user\n")
		}
	})

	t.Run("normalize_join/strips_extra_newlines", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "a.conf")
		p2 := filepath.Join(dir, "b.conf")
		os.WriteFile(p1, []byte("aaa\n\n\n"), 0644)
		os.WriteFile(p2, []byte("bbb"), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{
				{Name: "a.conf", Path: p1, Tags: []string{}},
				{Name: "b.conf", Path: p2, Tags: []string{}},
			},
			Target: DotTarget{Path: out},
		}
		gn := Generator{NormalizeJoin: true}
		if err := gn.Generate(entry, nil); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "aaa\n\nbbb\n" {
			t.Errorf("got %q, want %q", string(content), "aaa\n\nbbb\n")
		}
	})

	t.Run("normalize_join/adds_missing_newline", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "a.conf")
		p2 := filepath.Join(dir, "b.conf")
		os.WriteFile(p1, []byte("aaa"), 0644)
		os.WriteFile(p2, []byte("bbb"), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{
				{Name: "a.conf", Path: p1, Tags: []string{}},
				{Name: "b.conf", Path: p2, Tags: []string{}},
			},
			Target: DotTarget{Path: out},
		}
		gn := Generator{NormalizeJoin: true}
		if err := gn.Generate(entry, nil); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "aaa\n\nbbb\n" {
			t.Errorf("got %q, want %q", string(content), "aaa\n\nbbb\n")
		}
	})

	t.Run("normalize_join/collapses_excess_newlines", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "a.conf")
		os.WriteFile(p, []byte("aaa\n\n\nbbb"), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{{Name: "a.conf", Path: p, Tags: []string{}}},
			Target:  DotTarget{Path: out},
		}
		gn := Generator{NormalizeJoin: true}
		if err := gn.Generate(entry, nil); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "aaa\n\nbbb\n" {
			t.Errorf("got %q, want %q", string(content), "aaa\n\nbbb\n")
		}
	})

	t.Run("normalize_join/raw_preserves_content", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "a.conf")
		p2 := filepath.Join(dir, "b.conf")
		os.WriteFile(p1, []byte("aaa\n\n"), 0644)
		os.WriteFile(p2, []byte("bbb"), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{
				{Name: "a.conf", Path: p1, Tags: []string{}},
				{Name: "b.conf", Path: p2, Tags: []string{}},
			},
			Target: DotTarget{Path: out},
		}
		gr := Generator{NormalizeJoin: false}
		if err := gr.Generate(entry, nil); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "aaa\n\nbbb" {
			t.Errorf("got %q, want %q", string(content), "aaa\n\nbbb")
		}
	})

	t.Run("gtp/missingkey_zero", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config_gtp.conf")
		os.WriteFile(p, []byte(`val={{.undefined}}`), 0644)

		out := filepath.Join(dir, "out.conf")
		entry := DotEntry{
			Sources: []DotSource{
				{Name: "config_gtp.conf", Path: p, Tags: []string{"gtp"}},
			},
			Target: DotTarget{Path: out},
		}
		if err := g.Generate(entry, map[string]string{}); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "val=\n" {
			t.Errorf("got %q, want %q", string(content), "val=\n")
		}
	})
}

func TestNewApp(t *testing.T) {
	writeFile := func(t *testing.T, path string, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	boolPtr := func(b bool) *bool { return &b }

	t.Run("fallback_without_config", func(t *testing.T) {
		pwd := t.TempDir()

		app, err := NewApp(pwd, "", []string{"common"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if app.dotfilesDirPath != pwd {
			t.Errorf("dotfilesDirPath = %q, want %q", app.dotfilesDirPath, pwd)
		}
		if want := []EntrySpec{{Path: filepath.Join(pwd, "entry.yml")}}; !reflect.DeepEqual(app.entries, want) {
			t.Errorf("entries = %v, want %v", app.entries, want)
		}
		if want := []string{"common"}; !reflect.DeepEqual(app.polkaDirPaths, want) {
			t.Errorf("polkaDirPaths = %v, want %v", app.polkaDirPaths, want)
		}
	})

	t.Run("resolves_paths_relative_to_config", func(t *testing.T) {
		pwd := t.TempDir()
		root := t.TempDir()
		configPath := filepath.Join(root, "polkadot.yml")
		writeFile(t, configPath, "entries: [entry.yml, {path: hosts/a.yml, optional: true}]\ncomponents: [common, /abs/linux]\n")

		app, err := NewApp(pwd, configPath, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if app.dotfilesDirPath != root {
			t.Errorf("dotfilesDirPath = %q, want %q", app.dotfilesDirPath, root)
		}
		want := []EntrySpec{
			{Path: filepath.Join(root, "entry.yml")},
			{Path: filepath.Join(root, "hosts/a.yml"), Optional: true},
		}
		if !reflect.DeepEqual(app.entries, want) {
			t.Errorf("entries = %v, want %v", app.entries, want)
		}
		if want := []string{filepath.Join(root, "common"), "/abs/linux"}; !reflect.DeepEqual(app.polkaDirPaths, want) {
			t.Errorf("polkaDirPaths = %v, want %v", app.polkaDirPaths, want)
		}
	})

	t.Run("discovers_config_in_pwd", func(t *testing.T) {
		pwd := t.TempDir()
		writeFile(t, filepath.Join(pwd, "polkadot.yml"), "components: [common]\n")

		app, err := NewApp(pwd, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := []EntrySpec{{Path: filepath.Join(pwd, "entry.yml")}}; !reflect.DeepEqual(app.entries, want) {
			t.Errorf("entries = %v, want %v", app.entries, want)
		}
		if want := []string{filepath.Join(pwd, "common")}; !reflect.DeepEqual(app.polkaDirPaths, want) {
			t.Errorf("polkaDirPaths = %v, want %v", app.polkaDirPaths, want)
		}
	})

	t.Run("empty_entries_disables_default", func(t *testing.T) {
		pwd := t.TempDir()
		writeFile(t, filepath.Join(pwd, "polkadot.yml"), "entries: []\n")

		app, err := NewApp(pwd, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(app.entries) != 0 {
			t.Errorf("entries = %v, want empty", app.entries)
		}
	})

	t.Run("cli_overrides_config", func(t *testing.T) {
		pwd := t.TempDir()
		writeFile(t, filepath.Join(pwd, "polkadot.yml"), "components: [common]\nraw: true\n")

		app, err := NewApp(pwd, "", []string{"other"}, boolPtr(false))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"other"}; !reflect.DeepEqual(app.polkaDirPaths, want) {
			t.Errorf("polkaDirPaths = %v, want %v", app.polkaDirPaths, want)
		}
		if app.rawConcat {
			t.Error("expected -raw=false to override raw: true")
		}
	})

	t.Run("config_raw_applies", func(t *testing.T) {
		pwd := t.TempDir()
		writeFile(t, filepath.Join(pwd, "polkadot.yml"), "raw: true\n")

		app, err := NewApp(pwd, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !app.rawConcat {
			t.Error("expected raw: true to enable rawConcat")
		}
	})

	t.Run("rejects_invalid_entries", func(t *testing.T) {
		for _, content := range []string{
			"entries: [{path: a.yml, optinal: true}]\n",
			"entries: [{optional: true}]\n",
			"entries: [\"\"]\n",
			"entries: [[a.yml]]\n",
		} {
			pwd := t.TempDir()
			writeFile(t, filepath.Join(pwd, "polkadot.yml"), content)

			if _, err := NewApp(pwd, "", nil, nil); err == nil {
				t.Errorf("expected error for %q", content)
			}
		}
	})

	t.Run("empty_config_file", func(t *testing.T) {
		pwd := t.TempDir()
		writeFile(t, filepath.Join(pwd, "polkadot.yml"), "")

		app, err := NewApp(pwd, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := []EntrySpec{{Path: filepath.Join(pwd, "entry.yml")}}; !reflect.DeepEqual(app.entries, want) {
			t.Errorf("entries = %v, want %v", app.entries, want)
		}
	})

	t.Run("rejects_unknown_keys", func(t *testing.T) {
		pwd := t.TempDir()
		writeFile(t, filepath.Join(pwd, "polkadot.yml"), "component: [common]\n")

		if _, err := NewApp(pwd, "", nil, nil); err == nil {
			t.Error("expected error for unknown key")
		}
	})
}

func TestLoadEntry(t *testing.T) {
	t.Run("merges_in_order_with_inline_tags_last", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "entry.yml")
		host := filepath.Join(dir, "host.yml")
		os.WriteFile(base, []byte("linux:\narch:\nemacs: /usr/bin/emacs\n"), 0644)
		os.WriteFile(host, []byte("arch: \"!arch\"\neditor: vim\n"), 0644)
		app := App{
			entries:    []EntrySpec{{Path: base}, {Path: host}},
			inlineTags: map[string]string{"editor": "", "wsl": ""},
		}

		props, err := app.LoadEntry()
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"linux":  "linux",
			"arch":   "!arch",
			"emacs":  "/usr/bin/emacs",
			"editor": "editor",
			"wsl":    "wsl",
		}
		if !reflect.DeepEqual(props, want) {
			t.Errorf("props = %v, want %v", props, want)
		}
	})

	t.Run("missing_file_is_error", func(t *testing.T) {
		app := App{entries: []EntrySpec{{Path: filepath.Join(t.TempDir(), "nope.yml")}}}
		if _, err := app.LoadEntry(); err == nil {
			t.Error("expected error for missing entry file")
		}
	})

	t.Run("missing_optional_file_is_skipped", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "entry.yml")
		os.WriteFile(base, []byte("linux:\n"), 0644)
		app := App{entries: []EntrySpec{
			{Path: base},
			{Path: filepath.Join(dir, "entry.local.yml"), Optional: true},
		}}

		props, err := app.LoadEntry()
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"linux": "linux"}; !reflect.DeepEqual(props, want) {
			t.Errorf("props = %v, want %v", props, want)
		}
	})

	t.Run("existing_optional_file_is_loaded", func(t *testing.T) {
		dir := t.TempDir()
		local := filepath.Join(dir, "entry.local.yml")
		os.WriteFile(local, []byte("wsl:\n"), 0644)
		app := App{entries: []EntrySpec{{Path: local, Optional: true}}}

		props, err := app.LoadEntry()
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"wsl": "wsl"}; !reflect.DeepEqual(props, want) {
			t.Errorf("props = %v, want %v", props, want)
		}
	})
}

func TestGetVersion(t *testing.T) {
	t.Run("injected", func(t *testing.T) {
		orig := version
		t.Cleanup(func() { version = orig })
		version = "1.2.3"
		if got := getVersion(); got != "1.2.3" {
			t.Errorf("getVersion() = %q, want %q", got, "1.2.3")
		}
	})

	t.Run("fallback", func(t *testing.T) {
		orig := version
		t.Cleanup(func() { version = orig })
		version = ""
		if got := getVersion(); got == "" {
			t.Error("getVersion() returned empty string")
		}
	})
}

func TestWriteFileAtomic(t *testing.T) {
	listDir := func(t *testing.T, dir string) []string {
		t.Helper()
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		return names
	}

	t.Run("creates_file_with_mode", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "out")

		if err := writeFileAtomic(out, []byte("new\n"), 0640); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "new\n" {
			t.Errorf("got %q, want %q", content, "new\n")
		}
		st, _ := os.Stat(out)
		if st.Mode().Perm() != 0640 {
			t.Errorf("mode = %o, want %o", st.Mode().Perm(), 0640)
		}
		if names := listDir(t, dir); !reflect.DeepEqual(names, []string{"out"}) {
			t.Errorf("leftover files: %v", names)
		}
	})

	t.Run("replaces_existing_file_and_updates_mode", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "out")
		os.WriteFile(out, []byte("old content that is longer\n"), 0600)

		if err := writeFileAtomic(out, []byte("new\n"), 0644); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "new\n" {
			t.Errorf("got %q, want %q", content, "new\n")
		}
		st, _ := os.Stat(out)
		if st.Mode().Perm() != 0644 {
			t.Errorf("mode = %o, want %o", st.Mode().Perm(), 0644)
		}
	})

	t.Run("writes_through_symlink", func(t *testing.T) {
		dir := t.TempDir()
		real := filepath.Join(dir, "real")
		link := filepath.Join(dir, "link")
		os.WriteFile(real, []byte("old\n"), 0644)
		if err := os.Symlink(real, link); err != nil {
			t.Skip(err)
		}

		if err := writeFileAtomic(link, []byte("new\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
			t.Error("symlink was replaced by a regular file")
		}
		content, _ := os.ReadFile(real)
		if string(content) != "new\n" {
			t.Errorf("got %q, want %q", content, "new\n")
		}
	})

	t.Run("failure_keeps_existing_file", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "out")
		os.WriteFile(out, []byte("keep\n"), 0644)
		// A directory at the rename target makes the rename fail.
		target := filepath.Join(dir, "target")
		os.Mkdir(target, 0755)
		os.WriteFile(filepath.Join(target, "x"), nil, 0644)

		if err := writeFileAtomic(target, []byte("new\n"), 0644); err == nil {
			t.Fatal("expected error")
		}
		if names := listDir(t, dir); !reflect.DeepEqual(names, []string{"out", "target"}) {
			t.Errorf("leftover files: %v", names)
		}
		content, _ := os.ReadFile(out)
		if string(content) != "keep\n" {
			t.Errorf("got %q, want %q", content, "keep\n")
		}
	})
}

func TestLoadRulesValidation(t *testing.T) {
	load := func(t *testing.T, content string) (map[string]WeaverRule, error) {
		t.Helper()
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "rules.yml"), []byte(content), 0644)
		app := App{polkaDirPaths: []string{dir}}
		return app.LoadRules()
	}

	t.Run("accepts_valid_rules", func(t *testing.T) {
		rules, err := load(t, `
'distribute/.bashrc':
  dirs: ['sh.d', 'bash.d']
  pat: '\.(?:ba)?sh$'
'distribute/.local/bin/findup':
  dir: 'bin'
  pat: '^findup$'
  mode: 755
`)
		if err != nil {
			t.Fatal(err)
		}
		if mode := rules["distribute/.local/bin/findup"].Mode; mode == nil || *mode != 0755 {
			t.Errorf("mode = %v, want 0755", mode)
		}
	})

	for _, c := range []struct{ name, content string }{
		{"unknown_field", "~/.x:\n  dir: d\n  pattern: x\n"},
		{"missing_dir", "~/.x:\n  pat: x\n"},
		{"missing_pat", "~/.x:\n  dir: d\n"},
		{"mode_out_of_range", "~/.x:\n  dir: d\n  pat: x\n  mode: 1000\n"},
		{"invalid_pattern", "~/.x:\n  dir: d\n  pat: '('\n"},
		{"duplicate_key", "~/.x:\n  dir: d\n  pat: x\n~/.x:\n  dir: e\n  pat: y\n"},
	} {
		t.Run("rejects_"+c.name, func(t *testing.T) {
			if _, err := load(t, c.content); err == nil {
				t.Errorf("expected error for %q", c.content)
			}
		})
	}
}

func TestLoadPathsValidation(t *testing.T) {
	collect := func(t *testing.T, content string) (map[string]string, error) {
		t.Helper()
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "paths.yml"), []byte(content), 0644)
		app := App{polkaDirPaths: []string{dir}}
		return app.Collect()
	}

	t.Run("accepts_valid_paths", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		os.Mkdir(filepath.Join(home, ".cargo"), 0755)
		t.Setenv("POLKADOT_TEST_ENV", "value")

		props, err := collect(t, `
cargo:
  - type: dir
    path: ~/.cargo
fzf:
  - type: exec
    name: polkadot-no-such-command
myenv:
  - type: env
    name: POLKADOT_TEST_ENV
nothing:
`)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"cargo": filepath.Join(home, ".cargo"),
			"myenv": "value",
		}
		if !reflect.DeepEqual(props, want) {
			t.Errorf("props = %v, want %v", props, want)
		}
	})

	for _, c := range []struct{ name, content string }{
		{"unknown_field", "x:\n  - type: exec\n    nmae: y\n"},
		{"missing_type", "x:\n  - name: y\n"},
		{"unknown_type", "x:\n  - type: command\n"},
		{"dir_without_path", "x:\n  - type: dir\n"},
		{"file_without_path", "x:\n  - type: file\n"},
	} {
		t.Run("rejects_"+c.name, func(t *testing.T) {
			if _, err := collect(t, c.content); err == nil {
				t.Errorf("expected error for %q", c.content)
			}
		})
	}
}

func TestLoadYAML(t *testing.T) {
	t.Run("missing_file_is_not_exist", func(t *testing.T) {
		var v map[string]string
		err := loadYAML(filepath.Join(t.TempDir(), "nope.yml"), &v)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("components_skip_missing_files", func(t *testing.T) {
		app := App{polkaDirPaths: []string{t.TempDir()}}
		if _, err := app.LoadTags(); err != nil {
			t.Errorf("LoadTags: %v", err)
		}
		if _, err := app.LoadRules(); err != nil {
			t.Errorf("LoadRules: %v", err)
		}
		if _, err := app.Collect(); err != nil {
			t.Errorf("Collect: %v", err)
		}
	})

	t.Run("components_fail_on_unreadable_files", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"tags.yml", "rules.yml", "paths.yml"} {
			os.Mkdir(filepath.Join(dir, name), 0755) // a directory cannot be read as a file
		}
		app := App{polkaDirPaths: []string{dir}}
		if _, err := app.LoadTags(); err == nil {
			t.Error("LoadTags: expected error")
		}
		if _, err := app.LoadRules(); err == nil {
			t.Error("LoadRules: expected error")
		}
		if _, err := app.Collect(); err == nil {
			t.Error("Collect: expected error")
		}
	})
}
