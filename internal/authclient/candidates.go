package authclient

import (
	"context"
	"net/http"
	"net/url"
)

type credentialCandidateRequest struct {
	TokenIDs []string `json:"token_ids"`
}

type credentialCandidateResponse struct {
	TokenIDs []string `json:"token_ids"`
}

// CredentialCandidates asks MGR which local opaque credential identifiers may
// authorize the resolved endpoint. No secret is sent to MGR.
func (c *Client) CredentialCandidates(ctx context.Context, instanceID, endpointID string, tokenIDs []string) ([]string, error) {
	path := "/api/v1/instances/" + url.PathEscape(instanceID) + "/endpoints/" + url.PathEscape(endpointID) + "/credential-candidates"
	var response credentialCandidateResponse
	_, err := c.DoJSON(ctx, http.MethodPost, path, credentialCandidateRequest{TokenIDs: tokenIDs}, nil, &response)
	if err != nil {
		return nil, err
	}
	return response.TokenIDs, nil
}
