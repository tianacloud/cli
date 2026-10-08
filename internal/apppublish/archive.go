package apppublish

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxWebBytes int64 = 16 << 20
const maxWebResources = 4096

var errArchive = errors.New("invalid Web directory: require regular relative UTF-8 files within the 16 MiB and 4096-resource limits")

// Pack constructs the ZIP subset accepted by app_web without writing to the source.
// The root web.yaml is a separate, currently empty server configuration.
func packInto(dir string, destination io.Writer) error {
	root, e := filepath.Abs(dir)
	if e != nil {
		return errArchive
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errArchive
	}
	sandbox, e := os.OpenRoot(root)
	if e != nil {
		return errArchive
	}
	defer sandbox.Close()
	output := &archiveLimit{writer: destination}
	zw := zip.NewWriter(output)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.DefaultCompression) })
	configuration, e := zw.CreateHeader(&zip.FileHeader{Name: "web.yaml", Method: zip.Store})
	if e != nil {
		return errArchive
	}
	_, _ = configuration.Write(nil)
	var total int64
	count := 0
	e = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errArchive
		}
		if p == root {
			return nil
		}
		relative, e := filepath.Rel(root, p)
		if e != nil {
			return errArchive
		}
		relative = filepath.ToSlash(relative)
		if d.Type()&os.ModeSymlink != 0 {
			return errArchive
		}
		if d.IsDir() {
			return nil
		}
		if relative == "tiana.app.json" {
			return errors.New("tiana.app.json is retired; supply project parameters with web create")
		}
		if !validArchivePath(relative) || count >= maxWebResources {
			return errArchive
		}
		f, e := openArtifact(sandbox, relative)
		if e != nil {
			return errArchive
		}
		defer f.Close()
		stat, e := f.Stat()
		if e != nil || !stat.Mode().IsRegular() || stat.Size() > maxWebBytes-total {
			return errArchive
		}
		total += stat.Size()
		count++
		sample, e := io.ReadAll(io.LimitReader(f, 32768))
		if e != nil {
			return errArchive
		}
		compressed := &bytes.Buffer{}
		deflater, _ := flate.NewWriter(compressed, flate.DefaultCompression)
		_, _ = deflater.Write(sample)
		if deflater.Close() != nil {
			return errArchive
		}
		method := uint16(zip.Deflate)
		if compressed.Len()+18 >= len(sample) {
			method = zip.Store
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			return errArchive
		}
		header := &zip.FileHeader{Name: "data/" + relative, Method: method}
		header.SetMode(0644)
		writer, e := zw.CreateHeader(header)
		if e != nil {
			return errArchive
		}
		if n, e := io.CopyBuffer(writer, io.LimitReader(f, stat.Size()+1), make([]byte, 32768)); e != nil || n != stat.Size() {
			return errArchive
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = zw.Close(); e != nil {
		return errArchive
	}
	return nil
}

// Pack is a bounded in-memory helper for callers that explicitly need bytes.
// Publication uses PackFile and streams the private file to Gateway.
func Pack(dir string) ([]byte, string, error) {
	var output bytes.Buffer
	if e := packInto(dir, &output); e != nil {
		return nil, "", e
	}
	sum := sha256.Sum256(output.Bytes())
	return output.Bytes(), hex.EncodeToString(sum[:]), nil
}
func PackFile(dir string) (*os.File, string, error) {
	file, e := os.CreateTemp("", "tiana-web-*.tweb")
	if e != nil {
		return nil, "", errArchive
	}
	cleanup := func() { name := file.Name(); file.Close(); os.Remove(name) }
	hash := sha256.New()
	if e = packInto(dir, io.MultiWriter(file, hash)); e != nil {
		cleanup()
		return nil, "", e
	}
	if file.Sync() != nil {
		cleanup()
		return nil, "", errArchive
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		cleanup()
		return nil, "", errArchive
	}
	return file, hex.EncodeToString(hash.Sum(nil)), nil
}

func validArchivePath(p string) bool {
	if !utf8.ValidString(p) || len("data/"+p) > 1024 || strings.ContainsAny(p, "\\\x00\r\n") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

type archiveLimit struct {
	writer io.Writer
	size   int64
}

func (w *archiveLimit) Write(p []byte) (int, error) {
	if w.size+int64(len(p)) > maxWebBytes {
		return 0, errArchive
	}
	n, e := w.writer.Write(p)
	w.size += int64(n)
	return n, e
}

func ValidateArchiveEntry(file *os.File, entry string) error {
	if entry == "" {
		return nil
	}
	if !ValidEntry(entry) {
		return errors.New("invalid fixed project entry")
	}
	stat, e := file.Stat()
	if e != nil {
		return errArchive
	}
	z, e := zip.NewReader(file, stat.Size())
	if e != nil {
		return errArchive
	}
	for _, f := range z.File {
		if f.Name == "data/"+entry && !f.FileInfo().IsDir() {
			return nil
		}
	}
	return errors.New("archive is missing the fixed project entry: " + entry)
}
