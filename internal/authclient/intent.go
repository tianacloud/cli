package authclient

import "time"

// PendingCommandStore persists a local operation until its control-plane
// result is confirmed. It is deliberately local and has no server transport.
type PendingCommandStore interface {
	Load() (PendingCommand, error)
	Save(PendingCommand) error
	Delete() error
}

// PendingCommand records the resume state of one resource command. It never
// contains a raw InstanceToken.
type PendingCommand struct {
	Command        string    `json:"command"`
	Args           []string  `json:"args,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	Origin         string    `json:"origin"`
	UserID         string    `json:"user_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	// Step is the creation step a two-step `db create` reached: empty or
	// "instance" before the instance exists, "token" after it does.
	Step string `json:"step,omitempty"`
	// InstanceID is the resolved target for the token step. For a name
	// lookup it is stored so retries and authentication recovery never
	// re-resolve to a different instance.
	InstanceID string `json:"instance_id,omitempty"`
	// EndpointID pins the token write target across recovery. Older records
	// without it are filled from authoritative instance metadata before POST.
	EndpointID string `json:"endpoint_id,omitempty"`
	// TokenIdempotencyKey and TokenRequestID identify the token write that
	// may already be committed.
	TokenIdempotencyKey string `json:"token_idempotency_key,omitempty"`
	TokenRequestID      string `json:"token_request_id,omitempty"`
	TokenName           string `json:"token_name,omitempty"`
	ExpiresAt           int64  `json:"expires_at,omitempty"`
	// OperationID is set when a token write returned COMMIT_STATUS_UNKNOWN
	// and must be read back before any retry.
	OperationID string `json:"operation_id,omitempty"`
	JobID       uint64 `json:"job_id,omitempty"`
	TokenID     string `json:"token_id,omitempty"`
}
