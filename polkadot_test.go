package main

import (
	"reflect"
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

func TestMakeTagItem(t *testing.T) {
	tests := []struct {
		name       string
		rawTag     string
		value      string
		depth      int
		wantTag    string
		wantNeg    bool
		wantImport int
	}{
		{"plain tag", "linux", "linux", 0, "linux", false, 0},
		{"single negation", "!emacs", "!emacs", 0, "emacs", true, 1},
		{"double negation", "!!systemctl", "/usr/bin/systemctl", 1, "systemctl", false, 2},
		{"triple negation", "!!!foo", "bar", 2, "foo", true, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := makeTagItem(tt.rawTag, tt.value, tt.depth)
			if item.Tag != tt.wantTag {
				t.Errorf("Tag: got %q, want %q", item.Tag, tt.wantTag)
			}
			if item.Negative != tt.wantNeg {
				t.Errorf("Negative: got %v, want %v", item.Negative, tt.wantNeg)
			}
			if item.Importance != tt.wantImport {
				t.Errorf("Importance: got %d, want %d", item.Importance, tt.wantImport)
			}
			if item.Depth != tt.depth {
				t.Errorf("Depth: got %d, want %d", item.Depth, tt.depth)
			}
		})
	}
}

func TestToBasenameWithoutExt(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		recursive bool
		want      string
	}{
		{"simple file", "foo.txt", false, "foo"},
		{"no extension", "foo", false, "foo"},
		{"multiple extensions non-recursive", "foo.bar.txt", false, "foo.bar"},
		{"multiple extensions recursive", "foo.bar.txt", true, "foo"},
		{"with directory", "dir/foo.txt", false, "foo"},
		{"dot only", ".hidden", false, ""},  // filepath.Ext treats ".hidden" as the extension
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toBasenameWithoutExt(tt.path, tt.recursive)
			if got != tt.want {
				t.Errorf("toBasenameWithoutExt(%q, %v) = %q, want %q", tt.path, tt.recursive, got, tt.want)
			}
		})
	}
}

func TestExtractTagsFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{"no tags", "main.txt", []string{}},
		{"single tag", "main_linux.txt", []string{"linux"}},
		{"multiple tags", "main_linux_emacs.txt", []string{"linux", "emacs"}},
		{"nested path with tags", "subdir/main_gtp_arch.conf.txt", []string{"gtp", "arch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTagsFromPath(tt.path)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractTagsFromPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"absolute path unchanged", "/usr/bin/foo"},
		{"relative path unchanged", "relative/path"},
		{"no tilde", "notilde"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.path)
			if got != tt.path {
				t.Errorf("expandHome(%q) = %q, want %q (unchanged)", tt.path, got, tt.path)
			}
		})
	}
	t.Run("tilde expansion", func(t *testing.T) {
		got := expandHome("~/foo/bar")
		if got == "~/foo/bar" {
			t.Error("expandHome(\"~/foo/bar\") should expand the tilde")
		}
		if len(got) < len("/foo/bar") {
			t.Errorf("expandHome(\"~/foo/bar\") result %q is too short", got)
		}
	})
}

func TestRemoveDuplicatedDotSource(t *testing.T) {
	sources := []DotSource{
		{Name: "a", Path: "/path/a"},
		{Name: "b", Path: "/path/b"},
		{Name: "a2", Path: "/path/a"},
		{Name: "c", Path: "/path/c"},
		{Name: "b2", Path: "/path/b"},
	}
	got := removeDuplicatedDotSource(sources)
	if len(got) != 3 {
		t.Errorf("expected 3 unique sources, got %d", len(got))
	}
	wantPaths := []string{"/path/a", "/path/b", "/path/c"}
	for i, s := range got {
		if s.Path != wantPaths[i] {
			t.Errorf("source[%d].Path = %q, want %q", i, s.Path, wantPaths[i])
		}
	}
}
