package main

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := "web/templates"
	funcs := template.FuncMap{
		"fmtXMR":      func(int64) string { return "" },
		"fmtFiat":     func(int64, string) string { return "" },
		"fmtUSD":      func(int64) string { return "" },
		"inertURL":    func(string) template.HTML { return "" },
		"split":       strings.Split,
		"mul":         func(a, b int64) int64 { return a * b },
		"statusBadge": func(string) string { return "" },
	}
	all, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil { fmt.Println("glob:", err); os.Exit(1) }
	var extras []string
	for _, p := range all {
		b := filepath.Base(p)
		if b == "layout.html" || strings.HasPrefix(b, "page_") { continue }
		extras = append(extras, p)
	}
	bad := 0
	for _, p := range all {
		b := filepath.Base(p)
		if !strings.HasPrefix(b, "page_") { continue }
		files := append([]string{filepath.Join(dir, "layout.html")}, extras...)
		files = append(files, p)
		name := strings.TrimSuffix(b, ".html")
		if _, err := template.New(name).Funcs(funcs).ParseFiles(files...); err != nil {
			fmt.Printf("PARSE FAIL %s: %v\n", b, err)
			bad++
		}
	}
	if bad > 0 { os.Exit(1) }
	fmt.Println("all page templates parse cleanly")
}
