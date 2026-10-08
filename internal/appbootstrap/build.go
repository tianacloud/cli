// Package appbootstrap serves the platform-owned document and CSR application builds.
package appbootstrap

import (
	"errors"
	"github.com/tianacloud/cli/internal/apppublish"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const MaxFileSize = 16 << 20

// Manifest is a runtime descriptor derived from the MGR project; it is never a file.
type Manifest struct {
	WebID              string `json:"web_id"`
	Name               string `json:"name"`
	ProjectRevision    uint64 `json:"project_revision"`
	Rendering          string `json:"rendering"`
	Routing            string `json:"routing"`
	Entry              string `json:"entry"`
	DatabaseInstanceID string `json:"database_instance_id,omitempty"`
	GitInstanceID      string `json:"git_instance_id,omitempty"`
}
type Build struct {
	Manifest Manifest
	root     *os.Root
	files    map[string]bool
}

func (b *Build) Close() error { return b.root.Close() }

// LoadBuild validates the built output, not its framework or source tree.
// HTML belongs to Bootstrap; application builds contain modules and assets only.
func LoadBuild(dir string, descriptor Manifest) (_ *Build, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("cannot open application build directory")
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	m := descriptor
	params := apppublish.WebProjectParams{Entry: m.Entry, DatabaseInstanceID: m.DatabaseInstanceID, GitInstanceID: m.GitInstanceID}
	if params.Validate() != nil || m.Entry == "" || m.WebID == "" || strings.TrimSpace(m.Name) == "" || m.ProjectRevision == 0 {
		return nil, errors.New("preview requires the MGR project and its fixed Site entry")
	}
	m.Rendering, m.Routing = "csr", "hash"
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
		if rel == "tiana.app.json" {
			return errors.New("tiana.app.json is retired; project parameters come from MGR")
		}
		if !safePath(rel) || d.Type()&os.ModeSymlink != 0 {
			return errors.New("build paths must be relative, non-hidden and not symlinks")
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > MaxFileSize {
			return errors.New("build requires regular files up to 16 MiB")
		}
		ext := strings.ToLower(path.Ext(rel))
		if ext == ".html" || ext == ".htm" || ext == ".xhtml" {
			return errors.New("application HTML is not accepted: Bootstrap owns the document")
		}
		total += info.Size()
		if total > 16<<20 || len(b.files) >= 4096 {
			return errors.New("build exceeds 16 MiB or 4096 files")
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
