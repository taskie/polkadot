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
