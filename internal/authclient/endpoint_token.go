package authclient

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/localfile"
)

// LookupEndpointToken selects a uniquely scoped local credential for an explicit
// user-selected Endpoint. Unlike instance-mode lookup, this does not authorize
// through MGR. Gateway still authorizes the Token against the requested Endpoint.
// An optional origin restricts selection; ambiguity never triggers a guess.
func LookupEndpointToken(path, origin, endpointID string, now time.Time) (InstanceTokenCredential, error) {
	if endpointID == "" {
		return InstanceTokenCredential{}, ErrInstanceTokenNotFound
	}
	data, err := localfile.Read(path, 8*1024*1024, true)
	if errors.Is(err, os.ErrNotExist) {
		return InstanceTokenCredential{}, ErrInstanceTokenNotFound
	}
	if err != nil {
		return InstanceTokenCredential{}, errors.New("cannot read local InstanceTokens: require an owned, regular mode-0600 file, no symlinks, at most 8 MiB")
	}
	defer clear(data)
	// Decode only the identity index. The SDK remains responsible for Token and
	// expiry decoding (including legacy timestamps), validity and ordering.
	type identity struct {
		Origin     string `json:"origin"`
		TenantID   string `json:"tenant_id"`
		InstanceID string `json:"instance_id"`
		TokenID    string `json:"token_id"`
		EndpointID string `json:"endpoint_id"`
	}
	var file struct {
		Tokens map[string]identity `json:"tokens"`
	}
	if json.Unmarshal(data, &file) != nil {
		return InstanceTokenCredential{}, errors.New("invalid local InstanceToken store")
	}
	canonical := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	origin = canonical(origin)
	var selected identity
	for key, candidate := range file.Tokens {
		candidate.Origin = canonical(candidate.Origin)
		if candidate.EndpointID != endpointID || (origin != "" && candidate.Origin != origin) {
			continue
		}
		if candidate.Origin == "" || candidate.TenantID == "" || candidate.InstanceID == "" || candidate.TokenID == "" ||
			key != strings.Join([]string{candidate.Origin, candidate.TenantID, candidate.InstanceID, candidate.TokenID}, "|") {
			return InstanceTokenCredential{}, errors.New("inconsistent local InstanceToken identity")
		}
		if selected.Origin != "" && (selected.Origin != candidate.Origin || selected.TenantID != candidate.TenantID || selected.InstanceID != candidate.InstanceID) {
			return InstanceTokenCredential{}, errors.New("ambiguous local InstanceToken scope for endpoint; set TIANA_MGR_ORIGIN to select an environment or set TIANA_TOKEN explicitly")
		}
		selected = candidate
	}
	if selected.Origin == "" {
		return InstanceTokenCredential{}, ErrInstanceTokenNotFound
	}
	credential, err := NewFileInstanceTokenStore(path, selected.Origin).Lookup(selected.InstanceID, endpointID, now)
	if err != nil {
		return InstanceTokenCredential{}, err
	}
	// SDK lookup opens the file again. A concurrent atomic replacement must not
	// change the selected tenant between identity discovery and credential read.
	if canonical(credential.Origin) != selected.Origin || credential.TenantID != selected.TenantID || credential.InstanceID != selected.InstanceID || credential.EndpointID != endpointID {
		return InstanceTokenCredential{}, errors.New("local InstanceToken scope changed during lookup; retry explicitly")
	}
	return credential, nil
}
