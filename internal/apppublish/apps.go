package apppublish

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tianacloud/cli/internal/appbootstrap"
	"github.com/tianacloud/cli/internal/clientconfig"
)

// These are public MGR product API DTOs, not Control wire types.
type artifactFile struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	MD5         string `json:"md5"`
	SHA256      string `json:"sha256"`
}
type artifactManifest struct {
	Application *appbootstrap.Manifest `json:"application,omitempty"`
	EntryPath   string                 `json:"entry_path,omitempty"`
	Files       []artifactFile         `json:"files"`
}
type artifactVersion struct {
	TenantID    string           `json:"tenant_id"`
	ProjectID   string           `json:"project_id"`
	VersionID   string           `json:"version_id"`
	Manifest    artifactManifest `json:"manifest"`
	Fingerprint string           `json:"fingerprint"`
	State       string           `json:"state"`
}
type artifactLink struct {
	Path      string            `json:"path"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
	Uploaded  bool              `json:"uploaded"`
}

func appID(s string) bool {
	if len(s) == 0 || len(s) > 80 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func appError(code, message string) *Error {
	exit := 1
	if code == "PUBLISH_OUTCOME_UNKNOWN" {
		exit = 4
	}
	return &Error{Code: code, Message: message, NextAction: "Retry the same apps upload command to resume the same version", ExitCode: exit}
}
func (r Runner) Run(ctx context.Context, o Options) Result {
	if err := o.Validate(); err != nil {
		return Failure(err)
	}
	project, name, dir, version, entry, caFile := &o.Project, &o.Name, &o.Dir, &o.Version, &o.Entry, &o.UploadCAFile
	id, e := r.currentIdentity(ctx)
	if e != nil {
		return Failure(e)
	}

	base := "/api/v1/web-projects/" + url.PathEscape(*project)
	if o.Command == "create" {
		if *dir != "" || *version != "" || *entry != "" || *caFile != "" {
			return Failure(inputError("apps create accepts only --project, --name and --json"))
		}
		if *name == "" {
			*name = *project
		}
		res, e := r.request(ctx, id, "PUT", base, map[string]string{"name": *name})
		if e != nil {
			return Failure(e)
		}
		return Success(res.Body)
	}
	if *name != "" {
		return Failure(inputError("--name is only valid for apps create"))
	}
	if o.Command == "status" {
		if !appID(*version) || *dir != "" || *entry != "" || *caFile != "" {
			return Failure(inputError("Use apps status --project ID --version ID"))
		}
		res, e := r.request(ctx, id, "GET", base+"/versions/"+url.PathEscape(*version), nil)
		if e != nil {
			return Failure(e)
		}
		return Success(res.Body)
	}
	if *dir == "" || (*version != "" && !appID(*version)) {
		return Failure(inputError("Use apps upload --project ID --dir DIR [--version ID] [--entry PATH]"))
	}
	if *caFile == "" && r.UploadHTTP == nil {
		var roots *x509.CertPool
		if trust := clientconfig.FromContext(ctx); trust != nil {
			roots = trust.Roots
		}
		r.UploadHTTP = newUploadHTTPClient(roots)
	}
	if *caFile != "" {
		client, err := uploadClientWithCA(*caFile)
		if err != nil {
			return Failure(err)
		}
		r.UploadHTTP = client
	}
	root, err := os.OpenRoot(*dir)
	if err != nil {
		return Failure(appError("BUILD_DIRECTORY_UNAVAILABLE", "Cannot open the static output directory"))
	}
	defer root.Close()
	manifest, e := scanArtifacts(ctx, root, *entry)
	if e != nil {
		return Failure(e)
	}
	if manifest.Application != nil && manifest.Application.AppID != *project {
		return Failure(inputError("tiana.app.json app_id must match --project"))
	}
	raw, _ := json.Marshal(manifest)
	hash := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(hash[:])
	if *version == "" {
		*version = "v-" + fingerprint
	}
	base += "/versions/" + url.PathEscape(*version)
	fail := func(e *Error) Result {
		v := Failure(e)
		v.Data = map[string]string{"project_id": *project, "version_id": *version}
		return v
	}
	res, e := r.request(ctx, id, "PUT", base, manifest)
	if e != nil {
		return fail(e)
	}
	var prepared artifactVersion
	if json.Unmarshal(res.Body, &prepared) != nil || (prepared.TenantID == "" || (id.TenantID != "" && prepared.TenantID != id.TenantID)) || prepared.ProjectID != *project || prepared.VersionID != *version || prepared.Fingerprint != fingerprint || (prepared.State != "uploading" && prepared.State != "published") {
		return fail(appError("INVALID_UPLOAD_PLAN", "MGR returned an inconsistent version plan"))
	}
	// Published versions are immutable. A repeated command needs no file transfer.
	tenant := prepared.TenantID
	uploaded := 0
	if prepared.State != "published" {
		for offset := 0; offset < len(manifest.Files); offset += 16 {
			end := offset + 16
			if end > len(manifest.Files) {
				end = len(manifest.Files)
			}
			if e = r.uploadArtifactBatch(ctx, id, base, root, manifest.Files[offset:end]); e != nil {
				return fail(e)
			}
			uploaded += end - offset
		}
		// All local files were confirmed by OSS or the resumed HEAD check.
		// MGR checks only the configured entrypoints and publishes atomically.
		res, e = r.request(ctx, id, "POST", base+"/complete", struct{}{})
		if e != nil {
			return fail(e)
		}
		var next artifactVersion
		if json.Unmarshal(res.Body, &next) != nil || res.Status != http.StatusOK || next.TenantID != tenant || next.ProjectID != *project || next.VersionID != *version || next.Fingerprint != fingerprint || next.State != "published" {
			return fail(appError("PUBLISH_OUTCOME_UNKNOWN", "MGR has not confirmed publication of the same version"))
		}
		prepared = next

	}
	result := map[string]any{"project_id": *project, "version_id": *version, "state": prepared.State, "file_count": len(manifest.Files), "processed_files": uploaded, "entry_path": manifest.EntryPath}
	if manifest.Application != nil {
		res, e := r.request(ctx, id, "GET", base+"/bootstrap", nil)
		if e != nil {
			return fail(e)
		}
		var view struct {
			appbootstrap.Manifest
			VersionID      string `json:"version_id"`
			ApplicationURL string `json:"application_url"`
			VersionURL     string `json:"version_url"`
		}
		if json.Unmarshal(res.Body, &view) != nil || view.VersionID != *version || !reflect.DeepEqual(view.Manifest, *manifest.Application) || !validApplicationLinks(view.ApplicationURL, view.VersionURL, *project, *version) {
			return fail(appError("APP_HOSTING_UNCONFIRMED", "Artifacts are published but MGR has not confirmed matching Bootstrap metadata and HTTPS application links"))
		}
		result["application_url"], result["version_url"] = view.ApplicationURL, view.VersionURL
	}
	// Links confirm MGR's published binding. Live Web/CDN/Gateway acceptance is
	// still required; neither generic uploads nor this response prove SQL works.
	return Success(result)
}

func validApplicationLinks(app, pinned, project, version string) bool {
	u, err := url.Parse(app)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "/web/"+project+"/" {
		return false
	}
	return pinned == app+"?version="+version
}

func scanArtifacts(ctx context.Context, root *os.Root, entry string) (artifactManifest, *Error) {
	m := artifactManifest{EntryPath: entry, Files: []artifactFile{}}
	if _, err := root.Stat("tiana.app.json"); err == nil {
		if entry != "" {
			return m, inputError("Bootstrap owns HTML; --entry is not valid for CSR app builds")
		}
		build, err := appbootstrap.LoadBuild(root.Name())
		if err != nil {
			return m, inputError("Invalid CSR Bootstrap build: " + err.Error())
		}
		app := build.Manifest
		build.Close()
		m.Application = &app
	} else if !os.IsNotExist(err) {
		return m, inputError("Cannot inspect application manifest")
	}
	var total int64
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "." || (m.Application != nil && p == "tiana.app.json") {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links cannot be uploaded")
		}
		if d.IsDir() {
			return nil
		}
		if strings.Contains(p, `\`) || len(p) > 512 || path.Clean(p) != p {
			return fmt.Errorf("invalid relative path")
		}
		for _, c := range p {
			if unicode.IsControl(c) {
				return fmt.Errorf("invalid relative path")
			}
		}
		f, err := openArtifact(root, p)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size() > 128<<20 {
			return fmt.Errorf("file exceeds 128 MiB")
		}
		md, sha := md5.New(), sha256.New()
		n, err := io.Copy(io.MultiWriter(md, sha), io.LimitReader(f, (128<<20)+1))
		if err != nil || n != info.Size() {
			return fmt.Errorf("artifact changed during scan")
		}
		total += n
		if total > 1<<30 || len(m.Files) >= 1024 {
			return fmt.Errorf("release exceeds 1 GiB or 1024 files")
		}
		kind := mime.TypeByExtension(filepath.Ext(p))
		if kind == "" {
			kind = "application/octet-stream"
		}
		m.Files = append(m.Files, artifactFile{Path: p, Size: n, ContentType: kind, MD5: base64.StdEncoding.EncodeToString(md.Sum(nil)), SHA256: hex.EncodeToString(sha.Sum(nil))})
		return nil
	})
	if err != nil {
		return m, appError("INVALID_BUILD_OUTPUT", "Cannot scan build output: "+err.Error())
	}
	if len(m.Files) == 0 {
		return m, appError("INVALID_BUILD_OUTPUT", "The output directory contains no files")
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if entry != "" {
		found := false
		for _, f := range m.Files {
			if f.Path == entry {
				found = true
			}
		}
		if !found {
			return m, appError("INVALID_ENTRY_PATH", "The configured entry is not a file in the output directory")
		}
	}
	return m, nil
}

func (r Runner) uploadArtifactBatch(ctx context.Context, id identity, base string, root *os.Root, files []artifactFile) (failure *Error) {
	var planRequestID string
	defer func() {
		if failure != nil && failure.RequestID == "" {
			failure.RequestID = planRequestID
		}
	}()
	byPath := map[string]artifactFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	// Every retry asks MGR which objects exist. Never treat an ambiguous PUT as failure or success by itself.
	for attempt := 0; attempt < 3; attempt++ {
		res, e := r.request(ctx, id, "POST", base+"/uploads", map[string]any{"files": files})
		planRequestID = res.RequestID
		if e != nil {
			return e
		}
		var plan struct {
			Files []artifactLink `json:"files"`
		}
		if json.Unmarshal(res.Body, &plan) != nil || len(plan.Files) != len(files) {
			return appError("INVALID_UPLOAD_PLAN", "Invalid upload link batch")
		}
		seen := map[string]bool{}
		retry := false
		for _, link := range plan.Files {
			f, ok := byPath[link.Path]
			if !ok || seen[link.Path] {
				return appError("INVALID_UPLOAD_PLAN", "Upload plan contains an unexpected file")
			}
			seen[link.Path] = true
			if link.Uploaded {
				continue
			}
			if !link.ExpiresAt.After(time.Now().Add(time.Second)) {
				retry = true
				break
			}
			if e := r.putArtifact(ctx, root, f, link); e != nil {
				if e.Code == "ARTIFACT_CHANGED" || e.Code == "INVALID_UPLOAD_PLAN" {
					return e
				}
				retry = true
				break
			}
		}
		if !retry {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return appError("UPLOAD_INTERRUPTED", "Upload interrupted; rerun the same command")
			case <-timer.C:
			}
		}
	}
	return appError("UPLOAD_INCOMPLETE", "Upload did not finish after retries; rerun the same command")
}
func (r Runner) putArtifact(ctx context.Context, root *os.Root, file artifactFile, link artifactLink) *Error {
	u, err := url.Parse(link.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || link.Method != "PUT" {
		return appError("INVALID_UPLOAD_PLAN", "Expected a HTTPS PUT upload link")
	}
	f, err := openArtifact(root, file.Path)
	if err != nil {
		return appError("ARTIFACT_CHANGED", "Cannot reopen an artifact")
	}
	defer f.Close()
	md, sha := md5.New(), sha256.New()
	n, err := io.Copy(io.MultiWriter(md, sha), io.LimitReader(f, (128<<20)+1))
	if err != nil || n != file.Size || base64.StdEncoding.EncodeToString(md.Sum(nil)) != file.MD5 || hex.EncodeToString(sha.Sum(nil)) != file.SHA256 {
		return appError("ARTIFACT_CHANGED", "Build output changed after preparing this version")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return appError("ARTIFACT_CHANGED", "Cannot read the artifact")
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", link.URL, f)
	if err != nil {
		return appError("INVALID_UPLOAD_PLAN", "Invalid upload request")
	}
	req.ContentLength = file.Size
	if file.Size == 0 {
		req.Body = http.NoBody
	}
	for key, value := range link.Headers {
		if strings.EqualFold(key, "authorization") || strings.EqualFold(key, "cookie") || strings.EqualFold(key, "host") {
			return appError("INVALID_UPLOAD_PLAN", "Upload headers must not contain account credentials")
		}
		req.Header.Set(key, value)
	}
	if req.Header.Get("Content-MD5") != file.MD5 || req.Header.Get("Content-Type") != file.ContentType || req.Header.Get("X-Oss-Forbid-Overwrite") != "true" {
		return appError("INVALID_UPLOAD_PLAN", "Upload plan does not bind the artifact checksum, type and overwrite policy")
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	if r.UploadHTTP != nil {
		copy := *r.UploadHTTP
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	resp, err := client.Do(req)
	if err != nil {
		return appError("UPLOAD_RETRY", "Object upload did not return a confirmed response")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return appError("UPLOAD_RETRY", "Object upload was rejected; refreshing its plan")
	}
	return nil
}

func uploadClientWithCA(path string) (*http.Client, *Error) {
	trust, err := clientconfig.Load(path)
	if err != nil {
		return nil, appError("UPLOAD_CA_INVALID", "Upload CA must be a regular PEM file containing 1..8 certificates, at most 64 KiB")
	}
	return newUploadHTTPClient(trust.Roots), nil
}
