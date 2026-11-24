package apppublish

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type rawZipEntry struct {
	name          string
	content       []byte
	method        uint16
	flags         uint16
	externalAttrs uint32
	extra         []byte
	forgedSize    *uint32
}

func buildCustomZip(t *testing.T, entries []rawZipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		method := e.method
		if method == 0 && len(e.content) > 0 {
			method = zip.Store
		}
		h := &zip.FileHeader{
			Name:          e.name,
			Method:        method,
			Flags:         e.flags,
			ExternalAttrs: e.externalAttrs,
			Extra:         e.extra,
		}
		if e.externalAttrs == 0 {
			if strings.HasSuffix(e.name, "/") {
				h.SetMode(os.ModeDir | 0755)
			} else {
				h.SetMode(0644)
			}
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.content) > 0 {
			if _, err := w.Write(e.content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildRawZip creates a ZIP directly at byte level so adversarial tests can
// inject forged sizes, encrypted flags, unsupported methods, ZIP64 extra fields,
// multi-disk counts, etc. without Go's zip.Writer blocking them.
func buildRawZip(t *testing.T, entries []rawZipEntry, mutateEOCD func([]byte)) []byte {
	t.Helper()
	var out bytes.Buffer
	type cdRecord struct {
		entry       rawZipEntry
		localOffset uint32
		compData    []byte
		crc         uint32
		uncompSize  uint32
	}
	records := make([]cdRecord, 0, len(entries))

	for _, e := range entries {
		localOffset := uint32(out.Len())
		compData := e.content
		if e.method == zip.Deflate {
			var cb bytes.Buffer
			fw, err := flate.NewWriter(&cb, flate.BestCompression)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fw.Write(e.content); err != nil {
				t.Fatal(err)
			}
			if err := fw.Close(); err != nil {
				t.Fatal(err)
			}
			compData = cb.Bytes()
		}
		uncompSize := uint32(len(e.content))
		if e.forgedSize != nil {
			uncompSize = *e.forgedSize
		}

		// Local File Header (30 bytes)
		var lfh [30]byte
		binary.LittleEndian.PutUint32(lfh[0:4], 0x04034b50)
		binary.LittleEndian.PutUint16(lfh[4:6], 20)
		binary.LittleEndian.PutUint16(lfh[6:8], e.flags)
		binary.LittleEndian.PutUint16(lfh[8:10], e.method)
		binary.LittleEndian.PutUint32(lfh[14:18], crc32IEEE(e.content))
		binary.LittleEndian.PutUint32(lfh[18:22], uint32(len(compData)))
		binary.LittleEndian.PutUint32(lfh[22:26], uncompSize)
		binary.LittleEndian.PutUint16(lfh[26:28], uint16(len(e.name)))
		binary.LittleEndian.PutUint16(lfh[28:30], uint16(len(e.extra)))
		out.Write(lfh[:])
		out.WriteString(e.name)
		out.Write(e.extra)
		out.Write(compData)

		records = append(records, cdRecord{
			entry:       e,
			localOffset: localOffset,
			compData:    compData,
			crc:         crc32IEEE(e.content),
			uncompSize:  uncompSize,
		})
	}

	cdOffset := uint32(out.Len())
	for _, rec := range records {
		e := rec.entry
		var cd [46]byte
		binary.LittleEndian.PutUint32(cd[0:4], 0x02014b50)
		binary.LittleEndian.PutUint16(cd[4:6], (3<<8)|20) // Unix creator + v2.0
		binary.LittleEndian.PutUint16(cd[6:8], 20)
		binary.LittleEndian.PutUint16(cd[8:10], e.flags)
		binary.LittleEndian.PutUint16(cd[10:12], e.method)
		binary.LittleEndian.PutUint32(cd[16:20], rec.crc)
		binary.LittleEndian.PutUint32(cd[20:24], uint32(len(rec.compData)))
		binary.LittleEndian.PutUint32(cd[24:28], rec.uncompSize)
		binary.LittleEndian.PutUint16(cd[28:30], uint16(len(e.name)))
		binary.LittleEndian.PutUint16(cd[30:32], uint16(len(e.extra)))
		ext := e.externalAttrs
		if ext == 0 {
			if strings.HasSuffix(e.name, "/") {
				ext = (0o040755 << 16)
			} else {
				ext = (0o100644 << 16)
			}
		}
		binary.LittleEndian.PutUint32(cd[38:42], ext)
		binary.LittleEndian.PutUint32(cd[42:46], rec.localOffset)
		out.Write(cd[:])
		out.WriteString(e.name)
		out.Write(e.extra)
	}
	cdSize := uint32(out.Len()) - cdOffset

	var eocd [22]byte
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(eocd[8:10], uint16(len(records)))
	binary.LittleEndian.PutUint16(eocd[10:12], uint16(len(records)))
	binary.LittleEndian.PutUint32(eocd[12:16], cdSize)
	binary.LittleEndian.PutUint32(eocd[16:20], cdOffset)
	if mutateEOCD != nil {
		mutateEOCD(eocd[:])
	}
	out.Write(eocd[:])
	return out.Bytes()
}

func crc32IEEE(data []byte) uint32 {
	// Standard IEEE 802.3 CRC-32 polynomial 0xedb88320
	var crc uint32 = 0xffffffff
	for _, b := range data {
		crc ^= uint32(b)
		for range 8 {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xedb88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

func assertNoLeftoverTempEntries(t *testing.T, parent string, expected ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, name := range expected {
		want[name] = true
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Fatalf("unexpected leftover entry in %s: %q", parent, e.Name())
		}
	}
}

func TestExtractArchiveRoundTripWithPack(t *testing.T) {
	src := t.TempDir()
	files := map[string]string{
		"index.html":      strings.Repeat("<h1>Hello Tiana</h1>\n", 100), // Deflate
		"app.js":          "export function greet() {\n  return 'hi';\n}\n",
		"assets/logo.bin": string([]byte{0, 1, 2, 3, 4}), // Store
		"子目录/readme.txt":  "utf-8 path content\n",
	}
	for rel, content := range files {
		full := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}

	archiveBytes, _, err := Pack(src)
	if err != nil {
		t.Fatal(err)
	}

	parent := t.TempDir()
	dest := filepath.Join(parent, "pulled")
	got, err := extractArchive(t.Context(), bytes.NewReader(archiveBytes), int64(len(archiveBytes)), dest, "app.js")
	if err != nil {
		t.Fatalf("extractArchive failed: %v", err)
	}

	var wantBytes int64
	for rel, want := range files {
		wantBytes += int64(len(want))
		full := filepath.Join(dest, filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("missing extracted file %s: %v", rel, err)
		}
		if string(data) != want {
			t.Fatalf("file %s content mismatch: got %q want %q", rel, string(data), want)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(full)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0111 != 0 {
				t.Fatalf("file %s must not be executable: mode=%o", rel, info.Mode().Perm())
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(dest, "web.yaml")); !os.IsNotExist(err) {
		t.Fatalf("web.yaml must not be extracted to destination")
	}
	if got.FilesCount != len(files) || got.TotalBytes != wantBytes || got.LooksBundled {
		t.Fatalf("unexpected extracted stats: %+v (want files=%d bytes=%d looks_bundled=false)", got, len(files), wantBytes)
	}
	assertNoLeftoverTempEntries(t, parent, "pulled")
}

func TestExtractArchiveAllowsExplicitDirectoriesInAnyOrder(t *testing.T) {
	raw := buildRawZip(t, []rawZipEntry{
		{name: "web.yaml", method: zip.Store},
		{name: "data/", method: zip.Store, externalAttrs: 0o040755 << 16},
		{name: "data/sub/app.js", content: []byte("console.log(1);\n"), method: zip.Store},
		{name: "data/sub/", method: zip.Store, externalAttrs: 0o040755 << 16},
		{name: "data/empty/", method: zip.Store, externalAttrs: 0o040755 << 16},
	}, nil)

	parent := t.TempDir()
	dest := filepath.Join(parent, "out")
	got, err := extractArchive(t.Context(), bytes.NewReader(raw), int64(len(raw)), dest, "")
	if err != nil {
		t.Fatalf("extractArchive failed: %v", err)
	}
	if got.FilesCount != 1 || got.TotalBytes != int64(len("console.log(1);\n")) {
		t.Fatalf("unexpected stats: %+v", got)
	}
	if info, err := os.Stat(filepath.Join(dest, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("expected empty dir to be created: %v", err)
	}
}

func TestExtractArchiveAdversarialCasesRejectedAndCleaned(t *testing.T) {
	zero := uint32(0)
	tiny := uint32(16)
	zip64Extra := []byte{0x01, 0x00, 0x08, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}

	cases := []struct {
		name string
		data []byte
	}{
		{
			name: "dotdot traversal",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/../escape.txt", content: []byte("bad")},
			}),
		},
		{
			name: "nested dotdot traversal",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/a/../../escape.txt", content: []byte("bad")},
			}),
		},
		{
			name: "absolute path outside data",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "/etc/passwd", content: []byte("bad")},
			}),
		},
		{
			name: "leading slash inside data",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data//etc/passwd", content: []byte("bad")},
			}),
		},
		{
			name: "backslash path",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: `data/a\b.txt`, content: []byte("bad")},
			}),
		},
		{
			name: "colon in path",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/c:evil.txt", content: []byte("bad")},
			}),
		},
		{
			name: "control char in path",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/bad\nname.txt", content: []byte("bad")},
			}),
		},
		{
			name: "nul byte in path",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/bad\x00name.txt", content: []byte("bad")},
			}),
		},
		{
			name: "symlink unix mode",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/link", content: []byte("/etc/passwd"), externalAttrs: 0o120777 << 16},
			}, nil),
		},
		{
			name: "fifo unix mode",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/pipe", externalAttrs: 0o010644 << 16},
			}, nil),
		},
		{
			name: "socket unix mode",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/sock", externalAttrs: 0o140644 << 16},
			}, nil),
		},
		{
			name: "git directory segment",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/.git/config", content: []byte("[core]\n")},
			}),
		},
		{
			name: "uppercase GIT directory segment",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/nested/.GIT/hooks/post-checkout", content: []byte("#!/bin/sh\n")},
			}),
		},
		{
			name: "case-folded file collision",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/App.js", content: []byte("a")},
				{name: "data/app.js", content: []byte("b")},
			}),
		},
		{
			name: "case-folded directory collision",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/Assets/a.js", content: []byte("a")},
				{name: "data/assets/b.js", content: []byte("b")},
			}),
		},
		{
			name: "exact duplicate path",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/app.js", content: []byte("a")},
				{name: "data/app.js", content: []byte("b")},
			}, nil),
		},
		{
			name: "file then child prefix collision",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/foo", content: []byte("file")},
				{name: "data/foo/bar.js", content: []byte("child")},
			}),
		},
		{
			name: "child then file prefix collision",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/foo/bar.js", content: []byte("child")},
				{name: "data/foo", content: []byte("file")},
			}),
		},
		{
			name: "zip bomb with forged small header exceeding 16 MiB",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{
					name:       "data/bomb.txt",
					content:    bytes.Repeat([]byte("A"), int(maxWebBytes)+4096),
					method:     zip.Deflate,
					forgedSize: &tiny,
				},
			}, nil),
		},
		{
			name: "ZIP64 extra field rejected",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/a.txt", content: []byte("ok"), extra: zip64Extra},
			}, nil),
		},
		{
			name: "ZIP64 EOCD entry sentinel 65535 rejected",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/a.txt", content: []byte("ok")},
			}, func(eocd []byte) {
				binary.LittleEndian.PutUint16(eocd[8:10], 0xffff)
				binary.LittleEndian.PutUint16(eocd[10:12], 0xffff)
			}),
		},
		{
			name: "multi-disk archive rejected",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/a.txt", content: []byte("ok")},
			}, func(eocd []byte) {
				binary.LittleEndian.PutUint16(eocd[4:6], 1)
			}),
		},
		{
			name: "encrypted entry rejected",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/secret.txt", content: []byte("enc"), flags: 0x0001},
			}, nil),
		},
		{
			name: "unsupported flags rejected",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/flag.txt", content: []byte("x"), flags: 0x0040},
			}, nil),
		},
		{
			name: "unsupported compression method rejected",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "data/bzip.txt", content: []byte("x"), method: 12},
			}, nil),
		},
		{
			name: "missing web.yaml",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "data/index.html", content: []byte("hi")},
			}),
		},
		{
			name: "non-zero web.yaml",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml", content: []byte("key: value\n")},
				{name: "data/index.html", content: []byte("hi")},
			}),
		},
		{
			name: "forged zero-header non-zero web.yaml",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml", content: []byte("hidden"), method: zip.Deflate, forgedSize: &zero},
				{name: "data/index.html", content: []byte("hi")},
			}, nil),
		},
		{
			name: "duplicate web.yaml",
			data: buildRawZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "web.yaml"},
				{name: "data/index.html", content: []byte("hi")},
			}, nil),
		},
		{
			name: "unexpected root entry",
			data: buildCustomZip(t, []rawZipEntry{
				{name: "web.yaml"},
				{name: "evil.txt", content: []byte("nope")},
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "target")
			if _, err := extractArchive(t.Context(), bytes.NewReader(tc.data), int64(len(tc.data)), dest, ""); err == nil {
				t.Fatalf("expected adversarial archive %q to be rejected", tc.name)
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination %s must not exist on failure: %v", dest, err)
			}
			assertNoLeftoverTempEntries(t, parent)
		})
	}
}

func TestExtractArchiveTooManyFilesRejected(t *testing.T) {
	entries := make([]rawZipEntry, 0, maxWebResources+2)
	entries = append(entries, rawZipEntry{name: "web.yaml"})
	for i := 0; i < maxWebResources+1; i++ {
		entries = append(entries, rawZipEntry{
			name:    "data/f" + itoa(i) + ".txt",
			content: []byte("x"),
		})
	}
	raw := buildRawZip(t, entries, nil)
	parent := t.TempDir()
	dest := filepath.Join(parent, "target")
	if _, err := extractArchive(t.Context(), bytes.NewReader(raw), int64(len(raw)), dest, ""); err == nil {
		t.Fatal("expected > 4096 files to be rejected")
	}
	assertNoLeftoverTempEntries(t, parent)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestExtractArchiveExistingDestinationUntouched(t *testing.T) {
	raw := buildCustomZip(t, []rawZipEntry{
		{name: "web.yaml"},
		{name: "data/index.html", content: []byte("new")},
	})
	parent := t.TempDir()
	dest := filepath.Join(parent, "existing")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := extractArchive(t.Context(), bytes.NewReader(raw), int64(len(raw)), dest, "")
	if !errors.Is(err, errTargetAlreadyExists) {
		t.Fatalf("expected errTargetAlreadyExists, got %v", err)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "original" {
		t.Fatalf("existing destination was modified: %q %v", got, err)
	}
	assertNoLeftoverTempEntries(t, parent, "existing")
}

func TestLooksBundledHeuristic(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		files map[string]string
		want  bool
	}{
		{
			name:  "plain unbundled source",
			entry: "app.js",
			files: map[string]string{
				"index.html": "<!doctype html><html><body>Hi</body></html>\n",
				"app.js":     "export function init() {\n  console.log('hello');\n}\n",
				"style.css":  "body { color: #222; }\n",
			},
			want: false,
		},
		{
			name:  "package.json overrides hashed asset",
			entry: "app.js",
			files: map[string]string{
				"package.json":           `{"name":"my-app"}`,
				"dist/index-AbCd1234.js": "console.log(1);",
			},
			want: false,
		},
		{
			name:  "src directory overrides minified js",
			entry: "app.js",
			files: map[string]string{
				"src/main.ts": "export const x = 1;\n",
				"app.js":      strings.Repeat("a", 600),
			},
			want: false,
		},
		{
			name:  "content-hash js filename",
			files: map[string]string{"assets/index-AbCd1234.js": "console.log(1);\n"},
			want:  true,
		},
		{
			name:  "content-hash css filename",
			files: map[string]string{"assets/app-0123456789ab.css": "body{}\n"},
			want:  true,
		},
		{
			name:  "source map file",
			files: map[string]string{"app.js": "console.log(1);\n", "app.js.map": `{"version":3}`},
			want:  true,
		},
		{
			name:  "sourceMappingURL trailer in short file",
			files: map[string]string{"app.js": "console.log(1);\n//# sourceMappingURL=app.js.map\n"},
			want:  true,
		},
		{
			name:  "sourceMappingURL trailer past 4 KiB",
			files: map[string]string{"app.js": strings.Repeat("const a = 1;\n", 400) + "//# sourceMappingURL=app.js.map\n"},
			want:  true,
		},
		{
			name:  "minified single long line > 500 bytes",
			entry: "app.js",
			files: map[string]string{"app.js": "var x=" + strings.Repeat("1+", 260) + "0;"},
			want:  true,
		},
		{
			name:  "minified average line > 300 bytes over >= 1024 bytes",
			entry: "app.js",
			files: map[string]string{"app.js": strings.Repeat(strings.Repeat("a", 350)+"\n", 4)},
			want:  true,
		},
		{
			name:  "vite preload runtime token",
			files: map[string]string{"app.mjs": "const p = __vite_preload;\n"},
			want:  true,
		},
		{
			name:  "webpackChunk runtime token",
			files: map[string]string{"bundle.js": "(self.webpackChunk = self.webpackChunk || []).push([]);\n"},
			want:  true,
		},
		{
			name:  "agent-link-app.json runtime token",
			files: map[string]string{"app.js": "fetch('/agent-link-app.json');\n"},
			want:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]rawZipEntry, 0, len(tc.files)+1)
			entries = append(entries, rawZipEntry{name: "web.yaml"})
			for rel, content := range tc.files {
				entries = append(entries, rawZipEntry{name: "data/" + rel, content: []byte(content)})
			}
			raw := buildCustomZip(t, entries)
			dest := filepath.Join(t.TempDir(), "out")
			got, err := extractArchive(t.Context(), bytes.NewReader(raw), int64(len(raw)), dest, tc.entry)
			if err != nil {
				t.Fatalf("extractArchive failed: %v", err)
			}
			if got.LooksBundled != tc.want {
				t.Fatalf("looks_bundled=%v want %v", got.LooksBundled, tc.want)
			}
		})
	}
}

func TestCopyBoundedRejectsOversize(t *testing.T) {
	n, err := copyBounded(t.Context(), io.Discard, bytes.NewReader(bytes.Repeat([]byte("x"), 11)), 10)
	if !errors.Is(err, errStreamLimitExceeded) || n != 10 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = copyBounded(t.Context(), io.Discard, bytes.NewReader(bytes.Repeat([]byte("x"), 10)), 10)
	if err != nil || n != 10 {
		t.Fatalf("exact boundary failed: n=%d err=%v", n, err)
	}
}

func buildRawZipWithPrefix(t *testing.T, prefix []byte, entries []rawZipEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write(prefix)
	type cdRec struct {
		entry       rawZipEntry
		localOffset uint32
		compData    []byte
		crc         uint32
		uncompSize  uint32
	}
	records := make([]cdRec, 0, len(entries))
	for _, e := range entries {
		localOffset := uint32(out.Len())
		compData := e.content
		if e.method == zip.Deflate {
			var cb bytes.Buffer
			fw, err := flate.NewWriter(&cb, flate.BestCompression)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fw.Write(e.content); err != nil {
				t.Fatal(err)
			}
			if err := fw.Close(); err != nil {
				t.Fatal(err)
			}
			compData = cb.Bytes()
		}
		crc := crc32IEEE(e.content)
		uncompSize := uint32(len(e.content))
		var lfh [30]byte
		binary.LittleEndian.PutUint32(lfh[0:4], 0x04034b50)
		binary.LittleEndian.PutUint16(lfh[4:6], 20)
		binary.LittleEndian.PutUint16(lfh[6:8], e.flags)
		binary.LittleEndian.PutUint16(lfh[8:10], e.method)
		if e.flags&0x0008 == 0 {
			binary.LittleEndian.PutUint32(lfh[14:18], crc)
			binary.LittleEndian.PutUint32(lfh[18:22], uint32(len(compData)))
			binary.LittleEndian.PutUint32(lfh[22:26], uncompSize)
		}
		binary.LittleEndian.PutUint16(lfh[26:28], uint16(len(e.name)))
		binary.LittleEndian.PutUint16(lfh[28:30], uint16(len(e.extra)))
		out.Write(lfh[:])
		out.WriteString(e.name)
		out.Write(e.extra)
		out.Write(compData)
		if e.flags&0x0008 != 0 {
			var dd [12]byte
			binary.LittleEndian.PutUint32(dd[0:4], crc)
			binary.LittleEndian.PutUint32(dd[4:8], uint32(len(compData)))
			binary.LittleEndian.PutUint32(dd[8:12], uncompSize)
			out.Write(dd[:])
		}
		records = append(records, cdRec{
			entry:       e,
			localOffset: localOffset,
			compData:    compData,
			crc:         crc,
			uncompSize:  uncompSize,
		})
	}
	cdOffset := uint32(out.Len())
	for _, rec := range records {
		e := rec.entry
		var cd [46]byte
		binary.LittleEndian.PutUint32(cd[0:4], 0x02014b50)
		binary.LittleEndian.PutUint16(cd[4:6], (3<<8)|20)
		binary.LittleEndian.PutUint16(cd[6:8], 20)
		binary.LittleEndian.PutUint16(cd[8:10], e.flags)
		binary.LittleEndian.PutUint16(cd[10:12], e.method)
		binary.LittleEndian.PutUint32(cd[16:20], rec.crc)
		binary.LittleEndian.PutUint32(cd[20:24], uint32(len(rec.compData)))
		binary.LittleEndian.PutUint32(cd[24:28], rec.uncompSize)
		binary.LittleEndian.PutUint16(cd[28:30], uint16(len(e.name)))
		binary.LittleEndian.PutUint16(cd[30:32], uint16(len(e.extra)))
		binary.LittleEndian.PutUint32(cd[38:42], 0o100644<<16)
		binary.LittleEndian.PutUint32(cd[42:46], rec.localOffset)
		out.Write(cd[:])
		out.WriteString(e.name)
		out.Write(e.extra)
	}
	cdSize := uint32(out.Len()) - cdOffset
	var eocd [22]byte
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(eocd[8:10], uint16(len(records)))
	binary.LittleEndian.PutUint16(eocd[10:12], uint16(len(records)))
	binary.LittleEndian.PutUint32(eocd[12:16], cdSize)
	binary.LittleEndian.PutUint32(eocd[16:20], cdOffset)
	out.Write(eocd[:])
	return out.Bytes()
}

func TestExtractArchiveAllowsUnsignatured12ByteDataDescriptor(t *testing.T) {
	raw := buildRawZipWithPrefix(t, nil, []rawZipEntry{
		{name: "web.yaml", method: zip.Store, flags: 0x0008},
		{name: "data/index.html", content: []byte("<h1>ok</h1>\n"), method: zip.Deflate, flags: 0x0008},
	})
	parent := t.TempDir()
	dest := filepath.Join(parent, "out")
	got, err := extractArchive(t.Context(), bytes.NewReader(raw), int64(len(raw)), dest, "")
	if err != nil {
		t.Fatalf("expected 12-byte data descriptor archive to succeed: %v", err)
	}
	if got.FilesCount != 1 || got.TotalBytes != int64(len("<h1>ok</h1>\n")) {
		t.Fatalf("unexpected stats: %+v", got)
	}
}

func TestValidateRawCentralDirectoryLocalExtentAndHeaderAdversarial(t *testing.T) {
	// Helper to build a minimal valid raw ZIP and return offsets of each LFH and CD record.
	type zipParts struct {
		data       []byte
		lfhOffsets []int
		cdOffsets  []int
		cdStart    int
	}
	makeValid := func(entries []rawZipEntry) zipParts {
		var out bytes.Buffer
		var lfhOffsets []int
		type rec struct {
			e    rawZipEntry
			off  uint32
			comp []byte
			crc  uint32
		}
		var recs []rec
		for _, e := range entries {
			off := uint32(out.Len())
			lfhOffsets = append(lfhOffsets, int(off))
			comp := e.content
			if e.method == zip.Deflate {
				var cb bytes.Buffer
				fw, err := flate.NewWriter(&cb, flate.BestCompression)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fw.Write(e.content); err != nil {
					t.Fatal(err)
				}
				if err := fw.Close(); err != nil {
					t.Fatal(err)
				}
				comp = cb.Bytes()
			}
			crc := crc32IEEE(e.content)
			var lfh [30]byte
			binary.LittleEndian.PutUint32(lfh[0:4], 0x04034b50)
			binary.LittleEndian.PutUint16(lfh[4:6], 20)
			binary.LittleEndian.PutUint16(lfh[6:8], e.flags)
			binary.LittleEndian.PutUint16(lfh[8:10], e.method)
			binary.LittleEndian.PutUint32(lfh[14:18], crc)
			binary.LittleEndian.PutUint32(lfh[18:22], uint32(len(comp)))
			binary.LittleEndian.PutUint32(lfh[22:26], uint32(len(e.content)))
			binary.LittleEndian.PutUint16(lfh[26:28], uint16(len(e.name)))
			binary.LittleEndian.PutUint16(lfh[28:30], uint16(len(e.extra)))
			out.Write(lfh[:])
			out.WriteString(e.name)
			out.Write(e.extra)
			out.Write(comp)
			recs = append(recs, rec{e: e, off: off, comp: comp, crc: crc})
		}
		cdStart := out.Len()
		var cdOffsets []int
		for _, r := range recs {
			cdOffsets = append(cdOffsets, out.Len())
			var cd [46]byte
			binary.LittleEndian.PutUint32(cd[0:4], 0x02014b50)
			binary.LittleEndian.PutUint16(cd[4:6], (3<<8)|20)
			binary.LittleEndian.PutUint16(cd[6:8], 20)
			binary.LittleEndian.PutUint16(cd[8:10], r.e.flags)
			binary.LittleEndian.PutUint16(cd[10:12], r.e.method)
			binary.LittleEndian.PutUint32(cd[16:20], r.crc)
			binary.LittleEndian.PutUint32(cd[20:24], uint32(len(r.comp)))
			binary.LittleEndian.PutUint32(cd[24:28], uint32(len(r.e.content)))
			binary.LittleEndian.PutUint16(cd[28:30], uint16(len(r.e.name)))
			binary.LittleEndian.PutUint16(cd[30:32], uint16(len(r.e.extra)))
			binary.LittleEndian.PutUint32(cd[38:42], 0o100644<<16)
			binary.LittleEndian.PutUint32(cd[42:46], r.off)
			out.Write(cd[:])
			out.WriteString(r.e.name)
			out.Write(r.e.extra)
		}
		cdSize := uint32(out.Len() - cdStart)
		var eocd [22]byte
		binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)
		binary.LittleEndian.PutUint16(eocd[8:10], uint16(len(recs)))
		binary.LittleEndian.PutUint16(eocd[10:12], uint16(len(recs)))
		binary.LittleEndian.PutUint32(eocd[12:16], cdSize)
		binary.LittleEndian.PutUint32(eocd[16:20], uint32(cdStart))
		out.Write(eocd[:])
		return zipParts{data: out.Bytes(), lfhOffsets: lfhOffsets, cdOffsets: cdOffsets, cdStart: cdStart}
	}

	// 1. Two central-directory entries pointing to the same local header offset (aliased/duplicate offset)
	aliasedSameName := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Store},
		})
		// Point CD entry 2 at entry 1's local header offset so all local/central fields match.
		binary.LittleEndian.PutUint32(p.data[p.cdOffsets[2]+42:p.cdOffsets[2]+46], uint32(p.lfhOffsets[1]))
		return p.data
	}()

	// 2. Overlapping local data extents (entry1 compressed payload overlaps entry2's local header)
	overlappingExtent := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Deflate},
			{name: "data/b.txt", content: []byte("world-payload-padding"), method: zip.Store},
		})
		// Inflate entry 1's compressed size in BOTH LFH and CD so [start1, end1) overlaps lfhOffsets[2],
		// while still ending before cdStart.
		entry1Comp := binary.LittleEndian.Uint32(p.data[p.cdOffsets[1]+20 : p.cdOffsets[1]+24])
		inflatedComp := entry1Comp + 16
		binary.LittleEndian.PutUint32(p.data[p.lfhOffsets[1]+18:p.lfhOffsets[1]+22], inflatedComp)
		binary.LittleEndian.PutUint32(p.data[p.cdOffsets[1]+20:p.cdOffsets[1]+24], inflatedComp)
		return p.data
	}()

	// 3. Local/central filename mismatch (same length, different bytes)
	filenameMismatch := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Store},
		})
		copy(p.data[p.lfhOffsets[1]+30:p.lfhOffsets[1]+30+len("data/a.txt")], "data/b.txt")
		return p.data
	}()

	// 4. Local/central method mismatch
	methodMismatch := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Store},
		})
		binary.LittleEndian.PutUint16(p.data[p.lfhOffsets[1]+8:p.lfhOffsets[1]+10], zip.Deflate)
		return p.data
	}()

	// 5. Local/central flags mismatch
	flagsMismatch := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Store},
		})
		binary.LittleEndian.PutUint16(p.data[p.lfhOffsets[1]+6:p.lfhOffsets[1]+8], 0x0800)
		return p.data
	}()

	// 6. Local/central size mismatch (flags bit 3 clear)
	sizeMismatch := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Deflate},
		})
		binary.LittleEndian.PutUint32(p.data[p.lfhOffsets[1]+22:p.lfhOffsets[1]+26], 999)
		return p.data
	}()

	// 7. Self-extracting prefix bytes before first local header (offset > 0)
	sfxPrefix := buildRawZipWithPrefix(t, []byte("#!/bin/sh\necho sfx\n"), []rawZipEntry{
		{name: "web.yaml", method: zip.Store},
		{name: "data/index.html", content: []byte("hi"), method: zip.Store},
	})

	// 8. Local data extent reaching past central directory offset
	extendsPastCD := func() []byte {
		p := makeValid([]rawZipEntry{
			{name: "web.yaml", method: zip.Store},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Deflate},
		})
		entry1Comp := binary.LittleEndian.Uint32(p.data[p.cdOffsets[1]+20 : p.cdOffsets[1]+24])
		overshoot := entry1Comp + 1
		binary.LittleEndian.PutUint32(p.data[p.lfhOffsets[1]+18:p.lfhOffsets[1]+22], overshoot)
		binary.LittleEndian.PutUint32(p.data[p.cdOffsets[1]+20:p.cdOffsets[1]+24], overshoot)
		return p.data
	}()

	// 9. Data descriptor mismatch
	badDataDescriptor := func() []byte {
		raw := buildRawZipWithPrefix(t, nil, []rawZipEntry{
			{name: "web.yaml", method: zip.Store, flags: 0x0008},
			{name: "data/a.txt", content: []byte("hello"), method: zip.Store, flags: 0x0008},
		})
		// Corrupt the first entry's 12-byte data descriptor (immediately after "web.yaml" LFH).
		ddOff := 30 + len("web.yaml")
		binary.LittleEndian.PutUint32(raw[ddOff:ddOff+4], 0xdeadbeef)
		return raw
	}()

	cases := []struct {
		name       string
		data       []byte
		wantSubstr string
	}{
		{name: "aliased duplicate local offset", data: aliasedSameName, wantSubstr: "duplicate ZIP local header offset"},
		{name: "overlapping local data extents", data: overlappingExtent, wantSubstr: "overlapping ZIP local record extents"},
		{name: "local central filename mismatch", data: filenameMismatch, wantSubstr: "filename mismatch"},
		{name: "local central method mismatch", data: methodMismatch, wantSubstr: "compression method mismatch"},
		{name: "local central flags mismatch", data: flagsMismatch, wantSubstr: "flags mismatch"},
		{name: "local central size mismatch", data: sizeMismatch, wantSubstr: "size or CRC mismatch"},
		{name: "self extracting prefix", data: sfxPrefix, wantSubstr: "self-extracting ZIP prefix"},
		{name: "extent past central directory", data: extendsPastCD, wantSubstr: "extends past central directory"},
		{name: "data descriptor mismatch", data: badDataDescriptor, wantSubstr: "data descriptor mismatch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateRawCentralDirectory(bytes.NewReader(tc.data), int64(len(tc.data))); err == nil {
				t.Fatalf("expected validateRawCentralDirectory to reject %q", tc.name)
			} else if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("unexpected error for %q: got %q, want substring %q", tc.name, err.Error(), tc.wantSubstr)
			}

			parent := t.TempDir()
			dest := filepath.Join(parent, "target")
			if _, err := extractArchive(t.Context(), bytes.NewReader(tc.data), int64(len(tc.data)), dest, ""); err == nil {
				t.Fatalf("expected extractArchive to reject %q", tc.name)
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination %s must not exist on failure: %v", dest, err)
			}
			assertNoLeftoverTempEntries(t, parent)
		})
	}
}
