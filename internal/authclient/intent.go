package authclient

import "time"

// PendingCommandStore persists an instance creation until its result is reported.
type PendingCommandStore interface {
	Load() (PendingCommand, error)
	Save(PendingCommand) error
	Delete() error
}

// PendingFile pins the local input and accepted upload receipt for resumption.
type PendingFile struct {
	LocalPath   string `json:"local_path,omitempty"`
	RemotePath  string `json:"remote_path"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	Overwrite   bool   `json:"overwrite"`
	UploadID    string `json:"upload_id,omitempty"`
	Completed   bool   `json:"completed"`
}

// PendingCommand holds request identity and accepted receipts, never credentials.
type PendingCommand struct {
	File                *PendingFile `json:"file,omitempty"`
	Command             string       `json:"command"`
	Args                []string     `json:"args,omitempty"`
	IdempotencyKey      string       `json:"idempotency_key"`
	Origin              string       `json:"origin"`
	TenantID            string       `json:"tenant_id,omitempty"`
	UserID              string       `json:"user_id,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	InstanceID          string       `json:"instance_id,omitempty"`
	CreationJobID       uint64       `json:"creation_job_id,omitempty"`
	CreationOperationID string       `json:"creation_operation_id,omitempty"`
}
