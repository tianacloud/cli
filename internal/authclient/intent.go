package authclient

import "time"

// PendingCommandStore persists an instance creation until its result is reported.
type PendingCommandStore interface {
	Load() (PendingCommand, error)
	Save(PendingCommand) error
	Delete() error
}

// PendingCommand holds only the request identity and accepted creation receipt.
// It never contains credentials.
type PendingCommand struct {
	Command             string    `json:"command"`
	Args                []string  `json:"args,omitempty"`
	IdempotencyKey      string    `json:"idempotency_key"`
	Origin              string    `json:"origin"`
	UserID              string    `json:"user_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	InstanceID          string    `json:"instance_id,omitempty"`
	CreationJobID       uint64    `json:"creation_job_id,omitempty"`
	CreationOperationID string    `json:"creation_operation_id,omitempty"`
}
