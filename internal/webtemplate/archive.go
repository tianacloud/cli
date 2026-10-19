package webtemplate

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	MaxPackageBytes   int64 = 16 << 20
	MaxMaterialBytes  int64 = 16 << 20
	MaxExpandedBytes  int64 = 128 << 20
	MaxArchiveEntries       = 5000
)

type archiveBudget struct {
	ctx     context.Context
	bytes   int64
	entries int
}

// CopyBounded verifies the actual stream length even when metadata is missing
// or incorrect. The extra byte is observed but never written to the destination.
func CopyBounded(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, errors.New("negative stream budget")
	}
	r := contextReader{ctx, src}
	n, err := io.Copy(dst, io.LimitReader(r, limit))
	if err != nil {
		return n, err
	}
	var extra [1]byte
	read, err := io.ReadFull(r, extra[:])
	if read != 0 {
		return n, fmt.Errorf("material exceeds %d bytes", limit)
	}
	if err != nil && err != io.EOF {
		return n, err
	}
	return n, nil
}

// Check the actual central directory before archive/zip allocates File objects.
// Archives within these budgets use the ordinary, single-disk ZIP directory.
func archiveDirectory(r io.ReaderAt, size int64, remaining int) (int, error) {
	if size < 22 || size > MaxPackageBytes {
		return 0, errors.New("ZIP size outside package budget")
	}
	tailSize := min(size, 22+65535)
	tail := make([]byte, int(tailSize))
	if _, err := r.ReadAt(tail, size-tailSize); err != nil {
		return 0, err
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == 0x06054b50 && i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) == len(tail) {
			end = i
			break
		}
	}
	if end < 0 {
		return 0, zip.ErrFormat
	}
	e := tail[end:]
	count := int(binary.LittleEndian.Uint16(e[10:]))
	if binary.LittleEndian.Uint16(e[4:]) != 0 || binary.LittleEndian.Uint16(e[6:]) != 0 || int(binary.LittleEndian.Uint16(e[8:])) != count || count > remaining {
		return 0, errors.New("ZIP entry budget exceeded or invalid directory")
	}
	length := int64(binary.LittleEndian.Uint32(e[12:]))
	offset := int64(binary.LittleEndian.Uint32(e[16:]))
	if offset > size || length > size-offset || offset+length != size-tailSize+int64(end) {
		return 0, zip.ErrFormat
	}
	var header [46]byte
	actual := 0
	for cursor := offset; cursor < offset+length; {
		if offset+length-cursor < 46 {
			return 0, zip.ErrFormat
		}
		if _, err := r.ReadAt(header[:], cursor); err != nil {
			return 0, err
		}
		if binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return 0, zip.ErrFormat
		}
		actual++
		if actual > remaining {
			return 0, errors.New("ZIP exceeds entry budget")
		}
		next := cursor + 46 + int64(binary.LittleEndian.Uint16(header[28:])) + int64(binary.LittleEndian.Uint16(header[30:])) + int64(binary.LittleEndian.Uint16(header[32:]))
		if next > offset+length {
			return 0, zip.ErrFormat
		}
		cursor = next
	}
	if actual != count {
		return 0, zip.ErrFormat
	}
	return actual, nil
}

func (b *archiveBudget) open(r io.ReaderAt, size int64, source bool) (*zip.Reader, error) {
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	n, err := archiveDirectory(r, size, MaxArchiveEntries-b.entries)
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	if len(z.File) != n {
		return nil, zip.ErrFormat
	}
	b.entries += n
	files, dirs, seen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00:") {
			return nil, fmt.Errorf("invalid ZIP path %q", f.Name)
		}
		if !f.Mode().IsRegular() && !f.Mode().IsDir() {
			return nil, fmt.Errorf("ZIP entry must be a regular file or directory: %q", f.Name)
		}
		if source {
			for _, part := range strings.Split(name, "/") {
				if strings.EqualFold(part, ".git") {
					return nil, errors.New("source must exclude .git management files")
				}
			}
		}
		if seen[name] || files[name] || dirs[name] && !f.FileInfo().IsDir() {
			return nil, fmt.Errorf("conflicting ZIP path %q", name)
		}
		seen[name] = true
		if f.FileInfo().IsDir() {
			dirs[name] = true
		} else {
			files[name] = true
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if files[parent] {
				return nil, fmt.Errorf("ZIP file/directory collision %q", parent)
			}
			dirs[parent] = true
		}
		if f.UncompressedSize64 > uint64(MaxMaterialBytes) {
			return nil, fmt.Errorf("ZIP entry %q exceeds 16 MiB", name)
		}
	}
	return z, nil
}

func (b *archiveBudget) copy(dst io.Writer, f *zip.File, limit int64) (int64, error) {
	r, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	n, err := CopyBounded(b.ctx, dst, r, min(limit, MaxMaterialBytes, MaxExpandedBytes-b.bytes))
	b.bytes += n
	return n, err
}
