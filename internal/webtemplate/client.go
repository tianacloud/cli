package webtemplate

import (
	"net/http"

	"github.com/tianacloud/cli/internal/authclient"
)

type Variable struct {
	Name        string   `json:"name"`
	Placeholder string   `json:"placeholder"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
}
type Template struct {
	FileSeeds   string     `json:"file_seeds,omitempty"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Variables   []Variable `json:"variables,omitempty"`
}
type Client struct {
	Auth         *authclient.Client
	DownloadHTTP *http.Client
}
