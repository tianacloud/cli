package apppublish

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const (
	maxWebCDEntries        = maxWebResources*2 + 1
	maxBundledInspectFiles = 16
	maxBundledInspectBytes = 4096
	maxBundledTailBytes    = 512
)

var (
	errStreamLimitExceeded = errors.New("stream exceeds size limit")
	errTargetAlreadyExists = errors.New("target directory already exists")
	bundledAssetPattern    = regexp.MustCompile(`-[0-9A-Za-z_-]{8,}\.(?:m?js|css)$`)
	bundlerRuntimeTokens   = [][]byte{
		[]byte("__vite_preload"),
		[]byte("webpackChunk"),
		[]byte("webpackJsonp"),
		[]byte("agent-link-app.json"),
	}
	sourceMapMarkers = [][]byte{
		[]byte("//# sourceMappingURL="),
		[]byte("/*# sourceMappingURL="),
	}
)

type extractedArchive struct {
	FilesCount   int
	TotalBytes   int64
	LooksBundled bool
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

// copyBounded verifies the actual stream length even when headers or metadata
// are missing or forged. The extra byte is observed but never written to dst.
func copyBounded(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, errors.New("negative stream budget")
	}
	r := contextReader{ctx: ctx, reader: src}
	n, err := io.Copy(dst, io.LimitReader(r, limit))
	if err != nil {
		return n, err
	}
	var extra [1]byte
	read, err := io.ReadFull(r, extra[:])
	if read != 0 {
		return n, fmt.Errorf("%w (%d bytes)", errStreamLimitExceeded, limit)
	}
	if err != nil && err != io.EOF {
		return n, err
	}
	return n, nil
}

func requireAbsent(target string) error {
	_, err := os.Lstat(target)
	if err == nil {
		return errTargetAlreadyExists
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func isExistError(err error) bool {
	return errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY)
}

func validateExtraNoZIP64(extra []byte) error {
	for len(extra) > 0 {
		if len(extra) < 4 {
			return errors.New("malformed ZIP extra field")
		}
		id := binary.LittleEndian.Uint16(extra[0:2])
		sz := int(binary.LittleEndian.Uint16(extra[2:4]))
		if 4+sz > len(extra) {
			return errors.New("truncated ZIP extra field")
		}
		if id == 0x0001 {
			return errors.New("ZIP64 extra field is forbidden")
		}
		extra = extra[4+sz:]
	}
	return nil
}

// validateRawCentralDirectory parses the raw EOCD, central directory records,
// and local file header flags/extras before archive/zip allocates memory.
func validateRawCentralDirectory(r io.ReaderAt, size int64) (int, error) {
	if size < 22 || size > maxWebBytes {
		return 0, errors.New("invalid archive size")
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
	diskNum := binary.LittleEndian.Uint16(e[4:])
	cdDisk := binary.LittleEndian.Uint16(e[6:])
	diskEntries := binary.LittleEndian.Uint16(e[8:])
	totalEntries := binary.LittleEndian.Uint16(e[10:])
	if diskNum != 0 || cdDisk != 0 || diskEntries != totalEntries {
		return 0, errors.New("multi-disk ZIP archives are forbidden")
	}
	if totalEntries == 0 || totalEntries == 0xffff {
		return 0, errors.New("ZIP64 or empty archive is forbidden")
	}
	if int(totalEntries) > maxWebCDEntries {
		return 0, errors.New("ZIP archive exceeds maximum entry count")
	}
	cdSizeRaw := binary.LittleEndian.Uint32(e[12:])
	cdOffsetRaw := binary.LittleEndian.Uint32(e[16:])
	if cdSizeRaw == 0xffffffff || cdOffsetRaw == 0xffffffff {
		return 0, errors.New("ZIP64 central directory is forbidden")
	}
	cdSize := int64(cdSizeRaw)
	cdOffset := int64(cdOffsetRaw)
	eocdOffset := size - tailSize + int64(end)
	if cdOffset < 0 || cdSize < 0 || cdOffset > eocdOffset || cdSize != eocdOffset-cdOffset {
		return 0, zip.ErrFormat
	}

	var header [46]byte
	var lfh [30]byte
	actual := 0
	for cursor := cdOffset; cursor < eocdOffset; {
		if eocdOffset-cursor < 46 {
			return 0, zip.ErrFormat
		}
		if _, err := r.ReadAt(header[:], cursor); err != nil {
			return 0, err
		}
		if binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return 0, zip.ErrFormat
		}
		flags := binary.LittleEndian.Uint16(header[8:])
		if flags&0x1 != 0 || flags&^uint16(0x080e) != 0 {
			return 0, errors.New("encrypted or unsupported ZIP entry flags")
		}
		method := binary.LittleEndian.Uint16(header[10:])
		if method != zip.Store && method != zip.Deflate {
			return 0, errors.New("unsupported ZIP compression method")
		}
		if binary.LittleEndian.Uint16(header[34:]) != 0 {
			return 0, errors.New("multi-disk ZIP entry is forbidden")
		}
		nameLen := int64(binary.LittleEndian.Uint16(header[28:]))
		extraLen := int64(binary.LittleEndian.Uint16(header[30:]))
		commentLen := int64(binary.LittleEndian.Uint16(header[32:]))
		next := cursor + 46 + nameLen + extraLen + commentLen
		if next > eocdOffset {
			return 0, zip.ErrFormat
		}
		if extraLen > 0 {
			extra := make([]byte, int(extraLen))
			if _, err := r.ReadAt(extra, cursor+46+nameLen); err != nil {
				return 0, err
			}
			if err := validateExtraNoZIP64(extra); err != nil {
				return 0, err
			}
		}

		localOffsetRaw := binary.LittleEndian.Uint32(header[42:])
		if localOffsetRaw == 0xffffffff {
			return 0, errors.New("ZIP64 local header offset is forbidden")
		}
		localOffset := int64(localOffsetRaw)
		if localOffset < 0 || localOffset+30 > cdOffset {
			return 0, zip.ErrFormat
		}
		if _, err := r.ReadAt(lfh[:], localOffset); err != nil {
			return 0, err
		}
		if binary.LittleEndian.Uint32(lfh[:]) != 0x04034b50 {
			return 0, zip.ErrFormat
		}
		lfhFlags := binary.LittleEndian.Uint16(lfh[6:])
		if lfhFlags&0x1 != 0 || lfhFlags&^uint16(0x080e) != 0 {
			return 0, errors.New("encrypted or unsupported local ZIP entry flags")
		}
		lfhNameLen := int64(binary.LittleEndian.Uint16(lfh[26:]))
		lfhExtraLen := int64(binary.LittleEndian.Uint16(lfh[28:]))
		if localOffset+30+lfhNameLen+lfhExtraLen > cdOffset {
			return 0, zip.ErrFormat
		}
		if lfhExtraLen > 0 {
			lfhExtra := make([]byte, int(lfhExtraLen))
			if _, err := r.ReadAt(lfhExtra, localOffset+30+lfhNameLen); err != nil {
				return 0, err
			}
			if err := validateExtraNoZIP64(lfhExtra); err != nil {
				return 0, err
			}
		}

		actual++
		if actual > maxWebCDEntries {
			return 0, errors.New("ZIP archive exceeds maximum entry count")
		}
		cursor = next
	}
	if actual != int(totalEntries) {
		return 0, zip.ErrFormat
	}
	return actual, nil
}

func validateExtractedRelPath(rel, fullName string) error {
	if !utf8.ValidString(fullName) || len(fullName) > 1024 || len("data/"+rel) > 1024 {
		return fmt.Errorf("invalid archive path length or UTF-8: %q", fullName)
	}
	if rel == "" || rel == "." || rel == ".." {
		return fmt.Errorf("invalid archive relative path: %q", fullName)
	}
	if strings.HasPrefix(rel, "/") || strings.HasSuffix(rel, "/") {
		return fmt.Errorf("archive path must not have leading or trailing slash: %q", fullName)
	}
	if strings.ContainsAny(rel, "\\\x00:") {
		return fmt.Errorf("archive path contains forbidden character: %q", fullName)
	}
	for _, ch := range rel {
		if unicode.IsControl(ch) {
			return fmt.Errorf("archive path contains control character: %q", fullName)
		}
	}
	if path.IsAbs(rel) || path.Clean(rel) != rel || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return fmt.Errorf("archive path is not clean local relative path: %q", fullName)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("archive path contains invalid segment: %q", fullName)
		}
		if strings.EqualFold(seg, ".git") {
			return fmt.Errorf("archive must not contain .git path segment: %q", fullName)
		}
	}
	return nil
}

func verifyZeroByteEntry(ctx context.Context, f *zip.File) error {
	if f.UncompressedSize64 != 0 {
		return fmt.Errorf("archive entry %q must be 0 bytes", f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	n, copyErr := copyBounded(ctx, io.Discard, rc, 0)
	closeErr := rc.Close()
	if copyErr != nil || closeErr != nil || n != 0 {
		return fmt.Errorf("archive entry %q must be empty", f.Name)
	}
	return nil
}

func extractArchive(ctx context.Context, r io.ReaderAt, size int64, target, entry string) (extractedArchive, error) {
	if err := ctx.Err(); err != nil {
		return extractedArchive{}, err
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return extractedArchive{}, err
	}
	if err := requireAbsent(target); err != nil {
		return extractedArchive{}, err
	}
	parent := filepath.Dir(target)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return extractedArchive{}, errors.New("parent directory does not exist or is not a directory")
	}

	count, err := validateRawCentralDirectory(r, size)
	if err != nil {
		return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
	}
	if len(zr.File) != count {
		return extractedArchive{}, errors.New("invalid .tweb archive: entry count mismatch")
	}

	webYAMLCount := 0
	seenDataRoot := false
	regularCount := 0
	seenExact := map[string]bool{}
	lowerEntries := map[string]string{}
	files := map[string]bool{}
	dirs := map[string]bool{}
	lowerFiles := map[string]bool{}
	lowerDirs := map[string]string{}

	type plannedEntry struct {
		file  *zip.File
		rel   string
		isDir bool
	}
	planned := make([]plannedEntry, 0, len(zr.File))

	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return extractedArchive{}, err
		}
		if f.Flags&0x1 != 0 || f.Flags&^uint16(0x080e) != 0 {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: forbidden flags on %q", f.Name)
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: unsupported compression method on %q", f.Name)
		}
		if err := validateExtraNoZIP64(f.Extra); err != nil {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
		}
		unixType := (f.ExternalAttrs >> 16) & 0o170000
		if unixType != 0 && unixType != 0o100000 && unixType != 0o040000 {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: non-regular entry %q", f.Name)
		}
		if !f.Mode().IsRegular() && !f.Mode().IsDir() {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: unsupported file mode on %q", f.Name)
		}
		isDir := strings.HasSuffix(f.Name, "/")
		if isDir {
			if unixType == 0o100000 || !f.FileInfo().IsDir() {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: inconsistent directory entry %q", f.Name)
			}
		} else {
			if unixType == 0o040000 || f.FileInfo().IsDir() {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: inconsistent file entry %q", f.Name)
			}
		}

		if f.Name == "web.yaml" {
			if isDir {
				return extractedArchive{}, errors.New("invalid .tweb archive: web.yaml must be a regular file")
			}
			webYAMLCount++
			if webYAMLCount > 1 {
				return extractedArchive{}, errors.New("invalid .tweb archive: duplicate web.yaml")
			}
			if err := verifyZeroByteEntry(ctx, f); err != nil {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
			}
			continue
		}
		if f.Name == "data/" {
			if !isDir || seenDataRoot {
				return extractedArchive{}, errors.New("invalid .tweb archive: invalid or duplicate data/ entry")
			}
			seenDataRoot = true
			if err := verifyZeroByteEntry(ctx, f); err != nil {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
			}
			continue
		}
		if !strings.HasPrefix(f.Name, "data/") {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: unexpected root entry %q", f.Name)
		}

		rawRel := strings.TrimPrefix(f.Name, "data/")
		rel := rawRel
		if isDir {
			rel = strings.TrimSuffix(rawRel, "/")
		}
		if err := validateExtractedRelPath(rel, f.Name); err != nil {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
		}
		lower := strings.ToLower(rel)
		if seenExact[rel] {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: duplicate path %q", rel)
		}
		if prev, ok := lowerEntries[lower]; ok {
			return extractedArchive{}, fmt.Errorf("invalid .tweb archive: case-folded path collision between %q and %q", prev, rel)
		}
		if isDir {
			if files[rel] || lowerFiles[lower] {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: file/directory collision at %q", rel)
			}
			if prevDir, ok := lowerDirs[lower]; ok && prevDir != rel {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: case-folded directory collision between %q and %q", prevDir, rel)
			}
			dirs[rel] = true
			lowerDirs[lower] = rel
		} else {
			if dirs[rel] || lowerDirs[lower] != "" {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: file/directory collision at %q", rel)
			}
			regularCount++
			if regularCount > maxWebResources {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: exceeds %d files", maxWebResources)
			}
			files[rel] = true
			lowerFiles[lower] = true
		}
		seenExact[rel] = true
		lowerEntries[lower] = rel

		for ancestor := path.Dir(rel); ancestor != "."; ancestor = path.Dir(ancestor) {
			ancestorLower := strings.ToLower(ancestor)
			if files[ancestor] || lowerFiles[ancestorLower] {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: file/directory prefix collision at %q", ancestor)
			}
			if prevDir, ok := lowerDirs[ancestorLower]; ok && prevDir != ancestor {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: case-folded directory collision between %q and %q", prevDir, ancestor)
			}
			dirs[ancestor] = true
			lowerDirs[ancestorLower] = ancestor
		}

		planned = append(planned, plannedEntry{file: f, rel: rel, isDir: isDir})
	}

	if webYAMLCount != 1 {
		return extractedArchive{}, errors.New("invalid .tweb archive: missing web.yaml")
	}

	stageDir, err := os.MkdirTemp(parent, ".tiana-web-pull-dir-*")
	if err != nil {
		return extractedArchive{}, err
	}
	defer func() {
		if stageDir != "" {
			_ = os.RemoveAll(stageDir)
		}
	}()

	root, err := os.OpenRoot(stageDir)
	if err != nil {
		return extractedArchive{}, err
	}
	defer root.Close()

	var totalBytes int64
	filesCount := 0
	for _, item := range planned {
		if err := ctx.Err(); err != nil {
			return extractedArchive{}, err
		}
		if item.isDir {
			if err := verifyZeroByteEntry(ctx, item.file); err != nil {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: %w", err)
			}
			if err := root.MkdirAll(filepath.FromSlash(item.rel), 0755); err != nil {
				return extractedArchive{}, err
			}
			continue
		}
		if dir := path.Dir(item.rel); dir != "." {
			if err := root.MkdirAll(filepath.FromSlash(dir), 0755); err != nil {
				return extractedArchive{}, err
			}
		}
		out, err := root.OpenFile(filepath.FromSlash(item.rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return extractedArchive{}, err
		}
		rc, err := item.file.Open()
		if err != nil {
			_ = out.Close()
			return extractedArchive{}, err
		}
		n, copyErr := copyBounded(ctx, out, rc, maxWebBytes-totalBytes)
		rcErr := rc.Close()
		outErr := out.Close()
		if copyErr != nil {
			if errors.Is(copyErr, errStreamLimitExceeded) {
				return extractedArchive{}, fmt.Errorf("invalid .tweb archive: decompressed output exceeds %d bytes", maxWebBytes)
			}
			return extractedArchive{}, copyErr
		}
		if rcErr != nil {
			return extractedArchive{}, rcErr
		}
		if outErr != nil {
			return extractedArchive{}, outErr
		}
		totalBytes += n
		filesCount++
	}

	looksBundled := detectLooksBundled(root, files, dirs, entry)

	if err := root.Close(); err != nil {
		return extractedArchive{}, err
	}
	if err := ctx.Err(); err != nil {
		return extractedArchive{}, err
	}
	if err := requireAbsent(target); err != nil {
		return extractedArchive{}, err
	}
	if err := renameNoReplace(stageDir, target); err != nil {
		if isExistError(err) {
			return extractedArchive{}, errTargetAlreadyExists
		}
		return extractedArchive{}, err
	}
	stageDir = ""

	return extractedArchive{
		FilesCount:   filesCount,
		TotalBytes:   totalBytes,
		LooksBundled: looksBundled,
	}, nil
}

func detectLooksBundled(root *os.Root, files, dirs map[string]bool, entry string) bool {
	if files["package.json"] || dirs["src"] {
		return false
	}

	sortedFiles := make([]string, 0, len(files))
	for rel := range files {
		sortedFiles = append(sortedFiles, rel)
	}
	sort.Strings(sortedFiles)

	for _, rel := range sortedFiles {
		if strings.HasSuffix(strings.ToLower(rel), ".map") {
			return true
		}
		if bundledAssetPattern.MatchString(path.Base(rel)) {
			return true
		}
	}

	candidates := make([]string, 0, maxBundledInspectFiles)
	seenCandidate := map[string]bool{}
	if entry != "" && files[entry] && isInspectableBundleExt(entry) {
		candidates = append(candidates, entry)
		seenCandidate[entry] = true
	}
	for _, rel := range sortedFiles {
		if len(candidates) >= maxBundledInspectFiles {
			break
		}
		if seenCandidate[rel] || !isInspectableBundleExt(rel) {
			continue
		}
		candidates = append(candidates, rel)
		seenCandidate[rel] = true
	}

	for _, rel := range candidates {
		if fileLooksBundled(root, rel) {
			return true
		}
	}
	return false
}

func isInspectableBundleExt(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return ext == ".js" || ext == ".mjs" || ext == ".css"
}

func isJSExt(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return ext == ".js" || ext == ".mjs"
}

func fileLooksBundled(root *os.Root, rel string) bool {
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return false
	}
	defer f.Close()

	head, err := io.ReadAll(io.LimitReader(f, maxBundledInspectBytes))
	if err != nil || len(head) == 0 {
		return false
	}
	for _, marker := range sourceMapMarkers {
		if bytes.Contains(head, marker) {
			return true
		}
	}
	for _, token := range bundlerRuntimeTokens {
		if bytes.Contains(head, token) {
			return true
		}
	}
	if isJSExt(rel) && looksMinifiedJS(head) {
		return true
	}

	// Check file trailer for sourceMappingURL on files longer than maxBundledInspectBytes.
	if info, err := f.Stat(); err == nil && info.Size() > maxBundledInspectBytes {
		offset := info.Size() - maxBundledTailBytes
		if offset < 0 {
			offset = 0
		}
		if _, err := f.Seek(offset, io.SeekStart); err == nil {
			tail, err := io.ReadAll(io.LimitReader(f, maxBundledTailBytes))
			if err == nil {
				for _, marker := range sourceMapMarkers {
					if bytes.Contains(tail, marker) {
						return true
					}
				}
			}
		}
	}
	return false
}

func looksMinifiedJS(head []byte) bool {
	lines := bytes.Split(head, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) > 500 {
			return true
		}
	}
	if len(head) >= 1024 {
		lineCount := bytes.Count(head, []byte("\n"))
		if !bytes.HasSuffix(head, []byte("\n")) {
			lineCount++
		}
		if lineCount == 0 {
			lineCount = 1
		}
		if len(head) > 300*lineCount {
			return true
		}
	}
	return false
}
