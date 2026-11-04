package authclient

// FunctionRuntimeLog timestamps remain strings to preserve Loki nanoseconds.
type FunctionRuntimeLog struct {
	Timestamp string `json:"timestamp"`
	Stream    string `json:"stream"`
	Message   string `json:"message"`
}

type FunctionRuntimeLogPage struct {
	Items          []FunctionRuntimeLog `json:"items"`
	Start          string               `json:"start"`
	End            string               `json:"end"`
	NextCursor     string               `json:"next_cursor,omitempty"`
	RetentionHours int                  `json:"retention_hours"`
}
