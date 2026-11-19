package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/onionshop/internal/server"
)

func main() {
	dir := "/var/lib/onionshop/uploads"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "thumbs: read dir:", err)
		os.Exit(1)
	}
	made := 0
	for _, e := range entries {
		n := e.Name()
		lo := strings.ToLower(n)
		if strings.Contains(lo, "_thumb.") {
			continue
		}
		if !(strings.HasSuffix(lo, ".jpg") || strings.HasSuffix(lo, ".jpeg") || strings.HasSuffix(lo, ".png")) {
			continue
		}
		ok, err := server.EnsureThumbOnDisk(filepath.Join(dir, n))
		if err != nil {
			fmt.Fprintln(os.Stderr, "thumbs:", n, err)
			continue
		}
		if ok {
			made++
		}
	}
	fmt.Printf("thumbs created: %d\n", made)
}
