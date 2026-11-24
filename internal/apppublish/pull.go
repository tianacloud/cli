package apppublish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tianacloud/cli/internal/localfile"
	"github.com/tianacloud/sdk-go/fetch"
)

type PullResult struct {
	WebID         string `json:"web_id"`
	InstanceID    string `json:"instance_id"`
	Dir           string `json:"dir"`
	ServingSHA256 string `json:"serving_sha256"`
	Entry         string `json:"entry"`
	FilesCount    int    `json:"files_count"`
	TotalBytes    int64  `json:"total_bytes"`
	LooksBundled  bool   `json:"looks_bundled"`
}

func (r Runner) runPull(ctx context.Context, o Options) Result {
	if ctx.Err() != nil {
		return Failure(&Error{Code: "PULL_INTERRUPTED", Message: "Web archive pull was interrupted", NextAction: "Retry tiana web pull", ExitCode: 1})
	}
	target, err := filepath.Abs(o.Dir)
	if err != nil {
		return Failure(inputError("Invalid destination directory"))
	}
	if err := requireAbsent(target); err != nil {
		if errors.Is(err, errTargetAlreadyExists) {
			return Failure(&Error{Code: "TARGET_ALREADY_EXISTS", Message: "Destination path already exists", NextAction: "Choose a new directory that does not exist", ExitCode: 2})
		}
		return Failure(&Error{Code: "INVALID_DESTINATION", Message: "Cannot inspect destination path", NextAction: "Check destination path permissions", ExitCode: 2})
	}
	parent := filepath.Dir(target)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return Failure(inputError("Parent directory does not exist or is not a directory"))
	}

	session, e := r.openServerless(ctx, o.ID, "Web instance is not ready for pull")
	if e != nil {
		return Failure(e)
	}
	defer session.transport.Close()

	res, err := session.transport.Do(ctx, fetch.Request{Method: "GET", PathQuery: "/_tiana/web/archive", BodyLength: 0})
	if err != nil {
		return Failure(classifyPullTransportError(ctx, err))
	}
	defer res.Body.Close()

	if res.Status >= 300 {
		return Failure(classifyPullHTTPError(res))
	}
	if res.Status != http.StatusOK {
		return Failure(&Error{RequestID: res.RequestID, HTTPStatus: res.Status, Code: "INVALID_ARCHIVE_RESPONSE", Message: fmt.Sprintf("Unexpected archive response status (HTTP %d)", res.Status), NextAction: "Retry tiana web pull", ExitCode: 1})
	}

	declaredSha := strings.TrimSpace(res.Header.Get("X-Tiana-Web-Sha256"))
	if !isLowerHexSHA256(declaredSha) {
		return Failure(&Error{RequestID: res.RequestID, Code: "INVALID_ARCHIVE_RESPONSE", Message: "Missing or malformed X-Tiana-Web-Sha256 response header", NextAction: "Retry tiana web pull or verify the Web runtime", ExitCode: 1})
	}

	tmpFile, err := localfile.CreateTemp(parent, ".tiana-web-pull-*.tweb")
	if err != nil {
		return Failure(&Error{Code: "ARCHIVE_TEMP_UNAVAILABLE", Message: "Cannot create temporary file in destination parent directory", NextAction: "Check parent directory permissions", ExitCode: 1})
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	archiveSize, err := copyBounded(ctx, io.MultiWriter(tmpFile, hasher), res.Body, maxWebBytes)
	if err != nil {
		if ctx.Err() != nil {
			return Failure(&Error{RequestID: res.RequestID, Code: "PULL_INTERRUPTED", Message: "Web archive download was interrupted", NextAction: "Retry tiana web pull", ExitCode: 1})
		}
		if errors.Is(err, errStreamLimitExceeded) {
			return Failure(&Error{RequestID: res.RequestID, Code: "ARCHIVE_TOO_LARGE", Message: "Web archive exceeds the 16 MiB limit", NextAction: "Inspect the published application archive", ExitCode: 1})
		}
		return Failure(&Error{RequestID: res.RequestID, Code: "ARCHIVE_DOWNLOAD_FAILED", Message: "Failed to download Web archive", NextAction: "Retry tiana web pull", ExitCode: 1})
	}

	computedSha := hex.EncodeToString(hasher.Sum(nil))
	if computedSha != declaredSha {
		return Failure(&Error{RequestID: res.RequestID, Code: "ARCHIVE_DIGEST_MISMATCH", Message: "Downloaded Web archive SHA-256 does not match X-Tiana-Web-Sha256", NextAction: "Retry tiana web pull", ExitCode: 1})
	}

	extracted, err := extractArchive(ctx, tmpFile, archiveSize, target, session.project.Entry)
	if err != nil {
		if errors.Is(err, errTargetAlreadyExists) {
			return Failure(&Error{Code: "TARGET_ALREADY_EXISTS", Message: "Destination path already exists", NextAction: "Choose a new directory that does not exist", ExitCode: 2})
		}
		if ctx.Err() != nil {
			return Failure(&Error{RequestID: res.RequestID, Code: "PULL_INTERRUPTED", Message: "Web archive extraction was interrupted", NextAction: "Retry tiana web pull", ExitCode: 1})
		}
		return Failure(&Error{RequestID: res.RequestID, Code: "INVALID_ARCHIVE", Message: err.Error(), NextAction: "Inspect the published Web archive before retrying", ExitCode: 1})
	}

	return Success(PullResult{
		WebID:         session.project.ID,
		InstanceID:    session.instance.ID,
		Dir:           target,
		ServingSHA256: declaredSha,
		Entry:         session.project.Entry,
		FilesCount:    extracted.FilesCount,
		TotalBytes:    extracted.TotalBytes,
		LooksBundled:  extracted.LooksBundled,
	})
}

func classifyPullTransportError(ctx context.Context, err error) *Error {
	var fetchErr *fetch.Error
	if errors.As(err, &fetchErr) {
		if fetchErr.Status == http.StatusUnauthorized || fetchErr.Status == http.StatusForbidden {
			code := fetchErr.Code
			if code == "" {
				if fetchErr.Status == http.StatusUnauthorized {
					code = "AUTH_REQUIRED"
				} else {
					code = "WEB_PULL_FORBIDDEN"
				}
			}
			return &Error{
				RequestID:  fetchErr.RequestID,
				HTTPStatus: fetchErr.Status,
				Code:       code,
				Message:    "Not authorized to pull this Web archive",
				NextAction: "Check your login or token permissions",
				ExitCode:   5,
			}
		}
		code := fetchErr.Code
		if code == "" {
			code = "WEB_GATEWAY_FAILED"
		}
		return &Error{
			RequestID:  fetchErr.RequestID,
			HTTPStatus: fetchErr.Status,
			Code:       code,
			Message:    "Web archive download failed",
			NextAction: "Check application status and retry tiana web pull",
			ExitCode:   1,
		}
	}
	if ctx.Err() != nil {
		return &Error{Code: "PULL_INTERRUPTED", Message: "Web archive download was interrupted", NextAction: "Retry tiana web pull", ExitCode: 1}
	}
	return &Error{Code: "WEB_GATEWAY_FAILED", Message: "Web archive download failed", NextAction: "Check network connectivity and retry tiana web pull", ExitCode: 1}
}

func classifyPullHTTPError(res *fetch.Response) *Error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 65536))
	remoteCode, remoteMessage := parseControlErrorBody(raw)

	if res.Status == http.StatusNotFound {
		switch remoteCode {
		case "NOT_FOUND":
			return &Error{
				RequestID:  res.RequestID,
				HTTPStatus: http.StatusNotFound,
				Code:       "ARCHIVE_ENDPOINT_UNSUPPORTED",
				Message:    "This Web runtime does not support archive pull yet",
				NextAction: "Upgrade the Web runtime or use a local project folder",
				ExitCode:   1,
			}
		case "ARCHIVE_NOT_FOUND":
			return &Error{
				RequestID:  res.RequestID,
				HTTPStatus: http.StatusNotFound,
				Code:       "ARCHIVE_NOT_FOUND",
				Message:    "No published archive is available for this Web application",
				NextAction: "Publish the application with tiana web publish before pulling",
				ExitCode:   1,
			}
		}
	}

	if res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden {
		code := remoteCode
		if code == "" {
			if res.Status == http.StatusUnauthorized {
				code = "AUTH_REQUIRED"
			} else {
				code = "WEB_PULL_FORBIDDEN"
			}
		}
		message := remoteMessage
		if message == "" {
			message = "Not authorized to pull this Web archive"
		}
		return &Error{
			RequestID:  res.RequestID,
			HTTPStatus: res.Status,
			Code:       code,
			Message:    message,
			NextAction: "Check your login or token permissions",
			ExitCode:   5,
		}
	}

	code := remoteCode
	if code == "" {
		code = "WEB_CONTROL_FAILED"
	}
	message := remoteMessage
	if message == "" {
		message = fmt.Sprintf("Web control rejected the archive request (HTTP %d)", res.Status)
	}
	return &Error{
		RequestID:  res.RequestID,
		HTTPStatus: res.Status,
		Code:       code,
		Message:    message,
		NextAction: "Inspect application status with tiana web status",
		ExitCode:   1,
	}
}

func parseControlErrorBody(raw []byte) (code, message string) {
	if len(raw) == 0 {
		return "", ""
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return "", ""
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		if c, ok := nested["code"].(string); ok {
			code = strings.TrimSpace(c)
		}
		if m, ok := nested["message"].(string); ok {
			message = strings.TrimSpace(m)
		}
		return code, message
	}
	if c, ok := payload["code"].(string); ok {
		code = strings.TrimSpace(c)
	}
	if m, ok := payload["message"].(string); ok {
		message = strings.TrimSpace(m)
	}
	return code, message
}

func isLowerHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
