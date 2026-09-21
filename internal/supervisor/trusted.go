package supervisor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	trustedHelperRelativePath = "libexec/tiana/tiana-helper"
	trustedManifestName       = "tiana-helper.manifest.json"
)

type helperManifest struct {
	ContractVersion    uint16 `json:"contract_version"`
	HelperRelativePath string `json:"helper_relative_path"`
	SHA256             string `json:"sha256"`
	Platform           string `json:"platform"`
	Arch               string `json:"arch"`
}

// TrustedHelper is a verified companion path. Its path and bytes are checked
// before any process is started; cwd, PATH, and environment overrides are not
// considered.
type TrustedHelper struct {
	path     string
	manifest helperManifest
}

func (h TrustedHelper) Path() string { return h.path }

func (h TrustedHelper) ContractVersion() uint16 { return h.manifest.ContractVersion }

func ResolveTrustedHelper(installRoot string) (TrustedHelper, error) {
	if installRoot == "" {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	root, err := filepath.Abs(installRoot)
	if err != nil {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	if !trustedInstallChain(root) {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	manifestPath := filepath.Join(root, "libexec", "tiana", trustedManifestName)
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return TrustedHelper{}, err
	}
	if manifest.ContractVersion != HelperContractVersion || manifest.HelperRelativePath != trustedHelperRelativePath || manifest.Platform != runtime.GOOS || manifest.Arch != runtime.GOARCH {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	if !validDigest(manifest.SHA256) {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	helperPath := filepath.Join(root, filepath.FromSlash(trustedHelperRelativePath))
	if !insideRoot(root, helperPath) || !trustedRegular(helperPath) {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(helperPath)
		if statErr != nil || info.Mode().Perm()&0o111 == 0 {
			return TrustedHelper{}, ErrHelperNotTrusted
		}
	}
	digest, err := fileDigest(helperPath)
	if err != nil || !strings.EqualFold(hex.EncodeToString(digest[:]), manifest.SHA256) {
		return TrustedHelper{}, ErrHelperNotTrusted
	}
	return TrustedHelper{path: helperPath, manifest: manifest}, nil
}

func readManifest(path string) (helperManifest, error) {
	file, err := openTrustedRegular(path)
	if err != nil {
		return helperManifest{}, ErrHelperNotTrusted
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return helperManifest{}, ErrHelperNotTrusted
	}
	return decodeManifest(data)
}

func decodeManifest(data []byte) (helperManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest helperManifest
	if err := decoder.Decode(&manifest); err != nil {
		return helperManifest{}, ErrHelperNotTrusted
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return helperManifest{}, ErrHelperNotTrusted
	}
	return manifest, nil
}

func regularNonSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}

func trustedRegular(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && trustedOwner(info) && trustedWritable(info)
}

func trustedInstallChain(root string) bool {
	if !filepath.IsAbs(root) {
		return false
	}
	root = filepath.Clean(root)
	for path := root; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !trustedOwner(info) || !trustedWritable(info) {
			return false
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
	}
	for _, path := range []string{filepath.Join(root, "libexec"), filepath.Join(root, "libexec", "tiana")} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !trustedOwner(info) || !trustedWritable(info) {
			return false
		}
	}
	return true
}

func insideRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func fileDigest(path string) ([sha256.Size]byte, error) {
	file, err := openTrustedRegular(path)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("hash trusted helper: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 256<<20+1))
	if err != nil || len(data) > 256<<20 {
		return [sha256.Size]byte{}, fmt.Errorf("hash trusted helper: %w", err)
	}
	digest := sha256.Sum256(data)
	return digest, nil
}
