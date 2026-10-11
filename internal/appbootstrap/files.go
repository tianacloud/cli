package appbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

func (s *Server) fileStorage(w http.ResponseWriter, r *http.Request, identity Identity, suffix string) {
	if identity.Files == nil {
		s.failure(w, 503, "FILE_UNAVAILABLE")
		return
	}
	if suffix == "content" && (r.Method == "GET" || r.Method == "HEAD") {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		response, err := identity.Files.Get(r.Context(), r.Method, r.URL.Query().Get("path"), r.Header.Get("Range"))
		if err != nil {
			s.failure(w, 502, "FILE_DOWNLOAD_FAILED")
			return
		}
		defer response.Body.Close()
		for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Content-Disposition", "Content-Encoding", "ETag", "Last-Modified"} {
			if value := response.Header.Get(name); value != "" {
				w.Header().Set(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		if r.Method != "HEAD" {
			_, _ = io.Copy(w, response.Body)
		}
		return
	}
	if !previewFileRoute(suffix, r.Method) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var body json.RawMessage
	if r.Method == "POST" || r.Method == "DELETE" {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			s.failure(w, 400, "FILE_INPUT")
			return
		}
	}
	var result json.RawMessage
	path := "/" + suffix
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	var payload any
	if len(body) > 0 {
		payload = body
	}
	if err := identity.Files.Request(ctx, r.Method, path, payload, &result); err != nil {
		var api *authclient.APIError
		if errors.As(err, &api) {
			s.json(w, api.Status, map[string]any{"error": map[string]any{"code": api.Code, "message": "File request failed"}})
			return
		}
		s.failure(w, 503, "FILE_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(result)
}
func previewFileRoute(path, method string) bool {
	switch method {
	case "GET":
		if path == "capabilities" || path == "list" || path == "metadata" {
			return true
		}
	case "POST":
		if path == "read-grant" || path == "uploads" {
			return true
		}
	case "DELETE":
		return path == "object"
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "uploads" {
		return false
	}
	if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
		return false
	}
	return (len(parts) == 2 && method == "GET") || (len(parts) == 3 && method == "POST" && (parts[2] == "complete" || parts[2] == "cancel"))
}
