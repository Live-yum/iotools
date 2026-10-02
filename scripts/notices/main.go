// Collect notices from the selected, non-test dependency graph. Target flags
// affect only go list, so this collector can run on the build host.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type module struct {
	Path, Version, Dir string
	Main               bool
	Replace            *module
}

type packageInfo struct {
	ImportPath, Dir string
	Standard        bool
	Module          *module
	CFiles          []string
	CgoCFLAGS       []string
}

type licenseRecord struct {
	Module, Version, Source, File, SHA256 string
}

type target struct{ GOOS, GOARCH, CGOEnabled string }

type manifest struct {
	GoVersion string
	Targets   []target
	Packages  []string
	Licenses  []licenseRecord
}

type collector struct {
	out       string
	seenDirs  map[string]bool
	seenFiles map[string]string
	records   []licenseRecord
}

func main() {
	goos := flag.String("goos", "", "GOOS for go list (defaults to its environment)")
	goarch := flag.String("goarch", "", "comma-separated GOARCH targets for go list")
	cgo := flag.String("cgo", "", "CGO_ENABLED for go list (0 or 1)")
	flag.Parse()
	if flag.NArg() < 1 || (*cgo != "" && *cgo != "0" && *cgo != "1") {
		fmt.Fprintln(os.Stderr, "usage: notices [-goos OS] [-goarch ARCH[,ARCH]] [-cgo 0|1] OUTPUT_DIR [PACKAGE ...]")
		os.Exit(2)
	}
	packages := flag.Args()[1:]
	if len(packages) == 0 {
		packages = []string{"./cmd/iotools"}
	}
	if err := run(flag.Arg(0), packages, *goos, *goarch, *cgo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string, packages []string, goos, goarch, cgo string) error {
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	c := collector{out: out, seenDirs: map[string]bool{}, seenFiles: map[string]string{}}
	m := manifest{GoVersion: runtime.Version(), Packages: packages}
	for _, arch := range strings.Split(goarch, ",") {
		env := targetEnv(os.Environ(), goos, strings.TrimSpace(arch), cgo)
		cmd := exec.Command("go", "env", "GOOS", "GOARCH", "CGO_ENABLED")
		cmd.Env = env
		data, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("resolve target: %w", err)
		}
		values := strings.Fields(string(data))
		if len(values) != 3 {
			return fmt.Errorf("unexpected go env target: %q", data)
		}
		m.Targets = append(m.Targets, target{values[0], values[1], values[2]})
		args := append([]string{"list", "-deps", "-json"}, packages...)
		cmd = exec.Command("go", args...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("go list for %s/%s: %w\n%s", values[0], values[1], err, &stderr)
		}
		if err := c.collect(json.NewDecoder(bytes.NewReader(data))); err != nil {
			return err
		}
	}
	sort.Slice(c.records, func(i, j int) bool {
		a, b := c.records[i], c.records[j]
		return a.Module+"@"+a.Version+"/"+a.Source < b.Module+"@"+b.Version+"/"+b.Source
	})
	var index strings.Builder
	for _, r := range c.records {
		fmt.Fprintf(&index, "%s %s: %s -> %s\n", r.Module, r.Version, r.Source, r.File)
	}
	if err := os.WriteFile(filepath.Join(out, "INDEX.txt"), []byte(index.String()), 0644); err != nil {
		return err
	}
	m.Licenses = c.records
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "MANIFEST.json"), append(data, '\n'), 0644)
}

func targetEnv(env []string, goos, goarch, cgo string) []string {
	result := append([]string(nil), env...)
	for _, kv := range [][2]string{{"GOOS", goos}, {"GOARCH", goarch}, {"CGO_ENABLED", cgo}} {
		if kv[1] == "" {
			continue
		}
		prefix := kv[0] + "="
		filtered := result[:0]
		for _, value := range result {
			if !strings.HasPrefix(value, prefix) {
				filtered = append(filtered, value)
			}
		}
		result = append(filtered, prefix+kv[1])
	}
	return result
}

func (c *collector) collect(d *json.Decoder) error {
	modules := map[string]bool{}
	for {
		var pkg packageInfo
		if err := d.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		var root, name, version string
		switch {
		case pkg.Standard:
			root, name, version = runtime.GOROOT(), "Go", runtime.Version()
		case pkg.Module == nil || pkg.Module.Main:
			continue
		default:
			m := pkg.Module
			name, version, root = m.Path, m.Version, m.Dir
			if m.Replace != nil {
				root = m.Replace.Dir
				// Keep the selected module identity and record where its texts came from.
				name += " (replacement " + m.Replace.Path + " " + m.Replace.Version + ")"
			}
			if root == "" {
				return fmt.Errorf("module directory missing: %s", name)
			}
			modules[name] = true
		}
		if err := c.ancestors(root, pkg.Dir, name, version); err != nil {
			return err
		}
		if pkg.ImportPath == "github.com/mattn/go-sqlite3" && contains(pkg.CFiles, "sqlite3-binding.c") && !contains(pkg.CgoCFLAGS, "-DUSE_LIBSQLITE3") {
			if err := c.sqliteNotice(root, name, version); err != nil {
				return err
			}
		}
	}
	for name := range modules {
		found := false
		for _, r := range c.records {
			if r.Module == name && isLicenseName(filepath.Base(r.Source)) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("no license found for dependency %s", name)
		}
	}
	return nil
}

// Examine only the imported package and its ancestors, not unrelated testdata,
// examples, or packages that are excluded by the selected target's build tags.
func (c *collector) ancestors(root, dir, name, version string) error {
	root, dir = filepath.Clean(root), filepath.Clean(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("package directory %q is outside %q", dir, root)
	}
	for {
		key := name + "@" + version + ":" + dir
		if !c.seenDirs[key] {
			c.seenDirs[key] = true
			files, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, f := range files {
				if f.IsDir() || !isNoticeName(f.Name()) {
					continue
				}
				source, err := filepath.Rel(root, filepath.Join(dir, f.Name()))
				if err != nil {
					return err
				}
				data, err := os.ReadFile(filepath.Join(dir, f.Name()))
				if err != nil {
					return err
				}
				if err := c.write(name, version, filepath.ToSlash(source), data); err != nil {
					return err
				}
			}
		}
		if dir == root {
			return nil
		}
		dir = filepath.Dir(dir)
	}
}

func nameHasPrefix(name, prefix string) bool {
	name = strings.ToUpper(name)
	return name == prefix || strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, prefix+"_")
}

func isLicenseName(name string) bool {
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "EDL", "EPL"} {
		if nameHasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isNoticeName(name string) bool {
	if isLicenseName(name) {
		return true
	}
	for _, prefix := range []string{"NOTICE", "NOTICES", "AUTHORS", "COPYRIGHT", "PATENTS"} {
		if nameHasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// go-sqlite3's root LICENSE covers the Go wrapper. Its bundled SQLite C
// amalgamation carries a separate public-domain dedication in its source header.
// Preserve the complete upstream header comment, including its version, rather
// than substituting a hand-written license label.
func (c *collector) sqliteNotice(root, name, version string) error {
	data, err := os.ReadFile(filepath.Join(root, "sqlite3-binding.c"))
	if err != nil {
		return err
	}
	marker := []byte("/************** Begin file sqliteInt.h")
	boundary := bytes.Index(data, marker)
	if boundary < 0 {
		return fmt.Errorf("SQLite amalgamation header changed; review its notice extraction")
	}
	start := bytes.Index(data, []byte("/*"))
	end := bytes.Index(data[boundary:], []byte("*/"))
	if start < 0 || end < 0 {
		return fmt.Errorf("SQLite amalgamation notice header missing")
	}
	// The Begin file line itself ends with */; preserve the following full comment.
	next := boundary + end + 2
	commentStart := bytes.Index(data[next:], []byte("/*"))
	if commentStart < 0 {
		return fmt.Errorf("SQLite public-domain comment missing")
	}
	next += commentStart
	commentEnd := bytes.Index(data[next:], []byte("*/"))
	if commentEnd < 0 || !bytes.Contains(data[next:next+commentEnd], []byte("The author disclaims copyright")) {
		return fmt.Errorf("SQLite public-domain dedication changed; review its notice extraction")
	}
	introEnd := bytes.Index(data[start:], []byte("*/"))
	if introEnd < 0 {
		return fmt.Errorf("SQLite amalgamation version header missing")
	}
	notice := append([]byte(nil), data[start:start+introEnd+2]...)
	notice = append(notice, '\n', '\n')
	notice = append(notice, data[next:next+commentEnd+2]...)
	notice = append(notice, '\n')
	return c.write(name, version, "sqlite3-binding.c.header-notice.txt", notice)
}

func (c *collector) write(name, version, source string, data []byte) error {
	file := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name) + "@" + version + "_" + strings.ReplaceAll(source, "/", "__")
	if name == "Go" {
		file = "Go-" + strings.ReplaceAll(source, "/", "__")
		if source == "LICENSE" {
			file = "Go-LICENSE.txt"
		}
	}
	identity := name + "@" + version + ":" + source
	if previous, exists := c.seenFiles[file]; exists {
		if previous != identity {
			return fmt.Errorf("notice filename collision: %s and %s", previous, identity)
		}
		return nil
	}
	c.seenFiles[file] = identity
	if err := os.WriteFile(filepath.Join(c.out, file), data, 0644); err != nil {
		return err
	}
	c.records = append(c.records, licenseRecord{name, version, source, file, fmt.Sprintf("%x", sha256.Sum256(data))})
	return nil
}
