package authclient

import (
	"context"
	"net/http"
	"strconv"
)

type Job struct {
	InstanceID     string `json:"instance_id,omitempty"`
	JobID          uint64 `json:"job_id"`
	RequestID      string `json:"request_id"`
	JobKind        string `json:"job_kind"`
	Status         string `json:"status"`
	RetryJobID     uint64 `json:"retry_job_id,omitempty"`
	RetryCompleted bool   `json:"retry_completed,omitempty"`
}

func (c *Client) GetJob(ctx context.Context, jobID uint64) (Job, error) {
	var response Job
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/jobs/"+strconv.FormatUint(jobID, 10), nil, nil, &response)
	return response, err
}
