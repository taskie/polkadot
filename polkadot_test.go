package main

import (
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
	g := Generator{}

	t.Run("text/concatenation", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "a.conf")
		p2 := filepath.Join(dir, "b.conf")
		os.WriteFile(p1, []byte("hello "), 0644)
		os.WriteFile(p2, []byte("world"), 0644)

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
		if string(content) != "hello world" {
			t.Errorf("got %q, want %q", string(content), "hello world")
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
		if string(content) != "home=/home/user" {
			t.Errorf("got %q, want %q", string(content), "home=/home/user")
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
		if string(content) != "val=" {
			t.Errorf("got %q, want %q", string(content), "val=")
		}
	})
}
