package webtemplate

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Archive struct {
	Name   string
	Reader io.ReaderAt
	Size   int64
}

type Source struct {
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

type Result struct {
	Name         string `json:"name"`
	Directory    string `json:"directory"`
	MetadataPath string `json:"metadata_path"`
	Source       Source `json:"source,omitempty"`
	ZIPSize      int64  `json:"zip_size"`
	ZIPSHA256    string `json:"zip_sha256,omitempty"`
}

func Materialize(ctx context.Context, archive Archive, target string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return Result{}, err
	}
	if err := requireAbsent(target); err != nil {
		return Result{}, err
	}
	zr, err := zip.NewReader(archive.Reader, archive.Size)
	if err != nil {
		return Result{}, errors.New("template ZIP is invalid")
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return Result{}, err
	}
	temporary, err := os.MkdirTemp(parent, ".tiana-template-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(temporary)
	root, err := os.OpenRoot(temporary)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	for _, entry := range zr.File {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if strings.Contains(name, "\\") || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) {
			return Result{}, errors.New("template ZIP contains an invalid path")
		}
		if entry.FileInfo().IsDir() {
			if err := root.MkdirAll(filepath.FromSlash(name), 0755); err != nil {
				return Result{}, err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return Result{}, errors.New("template ZIP contains a nonregular file")
		}
		if err := root.MkdirAll(filepath.FromSlash(path.Dir(name)), 0755); err != nil {
			return Result{}, err
		}
		mode := entry.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		out, err := root.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return Result{}, err
		}
		in, err := entry.Open()
		if err != nil {
			out.Close()
			return Result{}, err
		}
		_, copyErr := io.Copy(out, contextReader{ctx, in})
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return Result{}, copyErr
		}
		if closeErr != nil {
			return Result{}, closeErr
		}
	}
	meta, err := root.Open("metadata.json")
	if err != nil {
		return Result{}, errors.New("template ZIP is missing metadata.json")
	}
	var manifest struct {
		Name   string `json:"name"`
		Source Source `json:"source"`
	}
	err = json.NewDecoder(meta).Decode(&manifest)
	meta.Close()
	if err != nil || manifest.Name != archive.Name {
		return Result{}, errors.New("template metadata name does not match the requested template")
	}
	if err := root.Close(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := requireAbsent(target); err != nil {
		return Result{}, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return Result{}, err
	}
	return Result{Name: archive.Name, Directory: target, MetadataPath: filepath.Join(target, "metadata.json"), Source: manifest.Source, ZIPSize: archive.Size}, nil
}

func requireAbsent(target string) error {
	_, err := os.Lstat(target)
	if err == nil {
		return errors.New("target directory already exists; choose a new directory")
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
