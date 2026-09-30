// Collect the exact dependency license texts alongside every binary artifact.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type module struct {
	Path, Version, Dir string
	Main               bool
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: notices OUTPUT_DIR")
	}
	out := os.Args[1]
	if e := os.MkdirAll(out, 0755); e != nil {
		panic(e)
	}
	cmd := exec.Command("go", "list", "-deps", "-json", "./cmd/iotools")
	b, e := cmd.Output()
	if e != nil {
		panic(e)
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	var index strings.Builder
	seen := map[string]bool{}
	for {
		var pkg struct{ Module *module }
		if e = d.Decode(&pkg); e == io.EOF {
			break
		} else if e != nil {
			panic(e)
		}
		if pkg.Module == nil {
			continue
		}
		m := *pkg.Module
		if seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		if m.Main {
			continue
		}
		dir := m.Dir
		if dir == "" {
			panic("module directory missing: " + m.Path)
		}
		files, e := os.ReadDir(dir)
		if e != nil {
			panic(e)
		}
		count := 0
		for _, f := range files {
			upper := strings.ToUpper(f.Name())
			if !f.IsDir() && (strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "COPYING") || upper == "NOTICE") {
				data, e := os.ReadFile(filepath.Join(dir, f.Name()))
				if e != nil {
					panic(e)
				}
				name := strings.ReplaceAll(m.Path, "/", "_") + "@" + m.Version + "_" + f.Name()
				if e = os.WriteFile(filepath.Join(out, name), data, 0644); e != nil {
					panic(e)
				}
				fmt.Fprintf(&index, "%s %s: %s\n", m.Path, m.Version, name)
				count++
			}
		}
		if count == 0 {
			panic("no license found for dependency " + m.Path)
		}
	}
	if e = os.WriteFile(filepath.Join(out, "INDEX.txt"), []byte(index.String()), 0644); e != nil {
		panic(e)
	}
}
