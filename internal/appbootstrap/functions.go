package appbootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// Preview uses the same selected account as the file broker. The Web binding is
// read at lookup time so editing the binding takes effect without restarting CLI.
func (s *Server) functionConnection(w http.ResponseWriter, r *http.Request, identity Identity) {
	var body struct {
		InstanceID string `json:"instance_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.InstanceID == "" {
		s.failure(w, 400, "INVALID_FUNCTION")
		return
	}
	if identity.FileRequest == nil {
		s.failure(w, 403, "FUNCTION_ACCESS_UNAVAILABLE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var binding struct {
		FunctionInstanceIDs []string `json:"function_instance_ids"`
	}
	if err := identity.FileRequest(ctx, "GET", "/api/v1/web-projects/"+url.PathEscape(s.config.Build.Manifest.WebID)+"/functions", nil, &binding); err != nil || !slices.Contains(binding.FunctionInstanceIDs, body.InstanceID) {
		s.failure(w, 403, "FUNCTION_ACCESS_UNAVAILABLE")
		return
	}
	var connection struct {
		InstanceID string `json:"instance_id"`
		Origin     string `json:"origin"`
		Token      string `json:"token"`
		ExpiresAt  int64  `json:"expires_at"`
	}
	if err := identity.FileRequest(ctx, "POST", "/api/v1/functions/"+url.PathEscape(body.InstanceID)+"/connection", map[string]string{"web_id": s.config.Build.Manifest.WebID}, &connection); err != nil {
		s.failure(w, 403, "FUNCTION_ACCESS_UNAVAILABLE")
		return
	}
	s.json(w, 200, connection)
}
