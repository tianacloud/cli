// Package appbootstrap serves the platform-owned document and CSR application builds.
package appbootstrap

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxFileSize = 128 << 20

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

type Manifest struct {
	SchemaVersion      int      `json:"schema_version"`
	AppID              string   `json:"app_id"`
	Name               string   `json:"name"`
	Rendering          string   `json:"rendering"`
	Routing            string   `json:"routing"`
	Entry              string   `json:"entry"`
	Styles             []string `json:"styles"`
	DatabaseInstanceID string   `json:"database_instance_id"`
}

type Build struct {
	Manifest Manifest
	root     *os.Root
	files    map[string]bool
}

func (b *Build) Close() error { return b.root.Close() }

// LoadBuild validates the built output, not its framework or source tree.
// HTML belongs to Bootstrap; application builds contain modules and assets only.
func LoadBuild(dir string) (_ *Build, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("cannot open application build directory")
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	f, err := root.Open("tiana.app.json")
	if err != nil {
		return nil, errors.New("build requires tiana.app.json")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32768 {
		return nil, errors.New("invalid application manifest")
	}
	var m Manifest
	dec := json.NewDecoder(io.LimitReader(f, 32769))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid application manifest")
	}
	if m.SchemaVersion != 1 || !identifier.MatchString(m.AppID) || len(m.Name) > 160 || strings.TrimSpace(m.Name) == "" || m.Rendering != "csr" || m.Routing != "hash" || !identifier.MatchString(m.DatabaseInstanceID) {
		return nil, errors.New("application requires schema 1, CSR, hash routing, app and database IDs")
	}
	if !safePath(m.Entry) || (path.Ext(m.Entry) != ".js" && path.Ext(m.Entry) != ".mjs") || len(m.Styles) > 32 {
		return nil, errors.New("invalid application module entry")
	}
	for _, s := range m.Styles {
		if !safePath(s) || path.Ext(s) != ".css" {
			return nil, errors.New("invalid application stylesheet")
		}
	}
	if m.Styles == nil {
		m.Styles = []string{}
	}
	b := &Build{Manifest: m, root: root, files: map[string]bool{}}
	var total int64
	err = filepath.WalkDir(dir, func(full string, d fs.DirEntry, e error) error {
		if e != nil {
			return errors.New("cannot inspect build files")
		}
		rel, e := filepath.Rel(dir, full)
		if e != nil {
			return errors.New("invalid build path")
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !safePath(rel) || d.Type()&os.ModeSymlink != 0 {
			return errors.New("build paths must be relative, non-hidden and not symlinks")
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > MaxFileSize {
			return errors.New("build requires regular files up to 128 MiB")
		}
		ext := strings.ToLower(path.Ext(rel))
		if ext == ".html" || ext == ".htm" || ext == ".xhtml" {
			return errors.New("application HTML is not accepted: Bootstrap owns the document")
		}
		total += info.Size()
		if total > 1<<30 || len(b.files) >= 1024 {
			return errors.New("build exceeds 1 GiB or 1024 files")
		}
		b.files[rel] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !b.files[m.Entry] {
		return nil, errors.New("application entry is missing")
	}
	for _, s := range m.Styles {
		if !b.files[s] {
			return nil, errors.New("application stylesheet is missing")
		}
	}
	delete(b.files, "tiana.app.json")
	return b, nil
}
func safePath(p string) bool {
	if !fs.ValidPath(p) || p == "." || len(p) > 512 || strings.ContainsAny(p, "\\\x00\r\n?#%:") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
