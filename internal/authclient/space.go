package authclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

type Space struct {
	TenantID           string `json:"tenant_id"`
	DisplayName        string `json:"display_name"`
	Revision           uint64 `json:"revision,string"`
	Role               string `json:"role,omitempty"`
	MembershipRevision uint64 `json:"membership_revision,string,omitempty"`
	IsDefault          bool   `json:"is_default"`
}

type SpaceState struct {
	Space
	CanManage   bool `json:"can_manage"`
	CanSetAdmin bool `json:"can_set_admin"`
}

type SpaceMember struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Revision    uint64 `json:"revision,string"`
}

type SpaceInvitation struct {
	ID          string    `json:"invitation_id"`
	TenantID    string    `json:"tenant_id"`
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name,omitempty"`
	Revision    uint64    `json:"revision,string"`
	State       string    `json:"state"`
	SendStatus  string    `json:"send_status"`
	ExpiresAt   time.Time `json:"expires_at"`
	TenantName  string    `json:"tenant_name,omitempty"`
}

type AccountState struct {
	User       User   `json:"user"`
	SyncStatus string `json:"sync_status"`
	JobID      uint64 `json:"job_id,omitempty"`
}

func (c *Client) AccountState(ctx context.Context) (AccountState, error) {
	var result AccountState
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/auth/transactions/whoami", nil, nil, &result)
	return result, err
}

func (c *Client) CurrentSpace(ctx context.Context) (SpaceState, error) {
	var result SpaceState
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/tenant", nil, nil, &result)
	if err == nil && (result.TenantID == "" || (c.credential != nil && result.TenantID != c.credential.User.TenantID)) {
		err = errors.New("space response does not match the selected space")
	}
	return result, err
}

func spaceList[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	items := make([]T, 0)
	after := ""
	seen := map[string]bool{}
	for page := 0; page < 10000; page++ {
		var result struct {
			Items      []T    `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		query := url.Values{"limit": {"100"}}
		if after != "" {
			query.Set("after", after)
		}
		if _, err := c.DoJSON(ctx, http.MethodGet, path+"?"+query.Encode(), nil, nil, &result); err != nil {
			return nil, err
		}
		items = append(items, result.Items...)
		if result.NextCursor == "" {
			return items, nil
		}
		if seen[result.NextCursor] || len(result.Items) == 0 {
			return nil, errors.New("invalid space list cursor")
		}
		seen[result.NextCursor] = true
		after = result.NextCursor
	}
	return nil, errors.New("space list exceeded the page limit")
}

func (c *Client) ListSpaces(ctx context.Context) ([]Space, error) {
	return spaceList[Space](ctx, c, "/api/v1/me/tenants")
}

func (c *Client) ListSpaceMembers(ctx context.Context) ([]SpaceMember, error) {
	return spaceList[SpaceMember](ctx, c, "/api/v1/tenant/members")
}

func (c *Client) ListSpaceInvitations(ctx context.Context) ([]SpaceInvitation, error) {
	return spaceList[SpaceInvitation](ctx, c, "/api/v1/tenant/invitations")
}

func (c *Client) CreateSpace(ctx context.Context, name, requestID string) (Space, error) {
	var result Space
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/me/tenants", map[string]string{"display_name": name}, map[string]string{"Idempotency-Key": requestID}, &result)
	if err == nil && result.TenantID == "" {
		err = errors.New("space creation response has no ID; inspect the original request")
	}
	return result, err
}

func (c *Client) RenameSpace(ctx context.Context, name string, revision uint64) (Space, error) {
	var result Space
	body := struct {
		Name     string `json:"display_name"`
		Revision uint64 `json:"expected_revision,string"`
	}{name, revision}
	_, err := c.DoJSON(ctx, http.MethodPatch, "/api/v1/tenant", body, nil, &result)
	if err == nil && (result.TenantID == "" || (c.credential != nil && result.TenantID != c.credential.User.TenantID)) {
		err = errors.New("space rename response has no matching ID")
	}
	return result, err
}

func (c *Client) LeaveSpace(ctx context.Context, tenantID string) (bool, error) {
	var result struct {
		LoggedOut *bool `json:"logged_out"`
	}
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/me/tenants/"+url.PathEscape(tenantID)+"/leave", struct{}{}, nil, &result)
	if err != nil {
		return false, err
	}
	if result.LoggedOut == nil {
		return false, errors.New("space exit response is incomplete")
	}
	return *result.LoggedOut, nil
}

func (c *Client) ChangeSpaceMember(ctx context.Context, userID string, revision uint64, admin bool) (SpaceMember, error) {
	method, path := http.MethodDelete, "/api/v1/tenant/members/"+url.PathEscape(userID)
	if admin {
		method, path = http.MethodPost, path+"/admin"
	}
	body := struct {
		Revision uint64 `json:"expected_revision,string"`
	}{revision}
	var result SpaceMember
	_, err := c.DoJSON(ctx, method, path, body, nil, &result)
	if err == nil && result.UserID != userID {
		err = errors.New("member response does not match the requested user")
	}
	return result, err
}

func (c *Client) InviteSpaceMember(ctx context.Context, userID string, revision uint64, requestID string) (SpaceInvitation, error) {
	body := struct {
		UserID   string `json:"user_id"`
		Revision uint64 `json:"expected_revision,string"`
	}{userID, revision}
	var result SpaceInvitation
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/tenant/invitations", body, map[string]string{"Idempotency-Key": requestID}, &result)
	if err == nil && (result.ID == "" || result.UserID != userID || (c.credential != nil && result.TenantID != c.credential.User.TenantID)) {
		err = errors.New("invitation response has no matching identity")
	}
	return result, err
}

func (c *Client) PreviewSpaceInvitation(ctx context.Context, id string) (SpaceInvitation, error) {
	var result SpaceInvitation
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/invitations/"+url.PathEscape(id)+"/preview", struct{}{}, nil, &result)
	if err == nil && (result.ID != id || result.TenantID == "") {
		err = errors.New("invitation preview has no matching identity")
	}
	return result, err
}

func (c *Client) AcceptSpaceInvitation(ctx context.Context, id string) (SpaceMember, error) {
	var result SpaceMember
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/invitations/"+url.PathEscape(id)+"/accept", struct{}{}, nil, &result)
	if err == nil && (result.UserID == "" || (c.credential != nil && result.UserID != c.credential.User.ID)) {
		err = errors.New("invitation acceptance has no matching member")
	}
	return result, err
}

// SpaceSwitch keeps credentials private; callers save them before reporting success.
type SpaceSwitch struct {
	Credential Credential `json:"-"`
	SyncStatus string     `json:"sync_status"`
	JobID      uint64     `json:"job_id,string,omitempty"`
}

func (c *Client) SwitchSpace(ctx context.Context, tenantID string) (SpaceSwitch, error) {
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		User         User   `json:"user"`
		SyncStatus   string `json:"sync_status"`
		JobID        uint64 `json:"job_id"`
	}
	_, err := c.DoJSON(ctx, http.MethodPost, "/api/v1/me/tenant", map[string]string{"tenant_id": tenantID}, nil, &result)
	if err != nil {
		return SpaceSwitch{}, err
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.ExpiresIn <= 0 || result.ExpiresIn > 365*24*3600 || result.User.ID == "" || result.User.TenantID != tenantID || (c.credential != nil && result.User.ID != c.credential.User.ID) {
		return SpaceSwitch{}, errors.New("space switch returned incomplete credentials; sign in again")
	}
	if result.TokenType == "" {
		result.TokenType = "Bearer"
	}
	if result.TokenType != "Bearer" {
		return SpaceSwitch{}, errors.New("unsupported authorization token type")
	}
	credential := Credential{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, TokenType: result.TokenType, ExpiresAt: c.config.Now().Add(time.Duration(result.ExpiresIn) * time.Second), User: result.User}
	return SpaceSwitch{Credential: credential, SyncStatus: result.SyncStatus, JobID: result.JobID}, nil
}

func (c *Client) SaveCredential(credential Credential) error { return c.config.Store.Save(credential) }
func (c *Client) DeleteCredential() error                    { return c.config.Store.Delete() }
