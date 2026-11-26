package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCSSClassContract is the P1 guardrail: every static class token used in a
// template must be defined in the single design-system stylesheet, and no
// template may reference the retired stylesheets (main.css / polish.css) or
// inline <style> blocks in layout.html. If you add a component class to a
// template, add it to web/static/shop.css in the same PR.
func TestCSSClassContract(t *testing.T) {
	const cssPath = "../../web/static/shop.css"
	cssBytes, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("read stylesheet: %v", err)
	}
	defined := map[string]bool{}
	classDef := regexp.MustCompile(`\.([a-zA-Z][a-zA-Z0-9_-]*)`)
	for _, m := range classDef.FindAllStringSubmatch(string(cssBytes), -1) {
		defined[m[1]] = true
	}

	pages, err := filepath.Glob("../../web/templates/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("template glob: %v (%d files)", err, len(pages))
	}
	// Match a full start-tag; then extract the class attribute from it. This
	// correctly handles `class="a {{if eq .X "b"}}c{{end}} d"` where the value
	// contains nested double quotes (the tag ends at the LAST quote before >).
	tagRE := regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9-]*)\s+([^<>]*?)/?>`)
	classRE := regexp.MustCompile(`class="(.*)"`)
	missing := map[string][]string{}
	for _, path := range pages {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, tag := range tagRE.FindAllStringSubmatch(string(b), -1) {
			m := classRE.FindStringSubmatch(tag[2])
			if m == nil {
				continue
			}
			for _, tok := range strings.Fields(m[1]) {
				if !defined[tok] {
					missing[tok] = append(missing[tok], filepath.Base(path))
				}
			}
		}
	}
	if len(missing) > 0 {
		var keys []string
		for k := range missing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Errorf("class %q used in %v but not defined in web/static/shop.css", k, dedupe(missing[k]))
		}
	}
}

// TestNoRetiredStylesheetRefs ensures the merged-away assets stay gone and the
// layout ships exactly one stylesheet with no inline <style> blocks.
func TestNoRetiredStylesheetRefs(t *testing.T) {
	layout, err := os.ReadFile("../../web/templates/layout.html")
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	s := string(layout)
	for _, banned := range []string{"main.css", "polish.", "<style"} {
		if strings.Contains(s, banned) {
			t.Errorf("layout.html still references %q; single stylesheet is /static/shop.css", banned)
		}
	}
	if n := strings.Count(s, `<link rel="stylesheet"`); n != 1 {
		t.Errorf("layout.html has %d stylesheet links, want exactly 1", n)
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
