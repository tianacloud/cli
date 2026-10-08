package authclient

import (
	"context"
	"net/http"
)

type appTypeCatalog struct {
	Items []struct {
		Engine string `json:"engine"`
	} `json:"items"`
}

// ListAppTypes returns the database engines published in this MGR
// environment's App catalog. The result is empty when nothing is published.
func (c *Client) ListAppTypes(ctx context.Context) ([]string, error) {
	var response appTypeCatalog
	_, err := c.DoJSON(ctx, http.MethodGet, "/api/v1/app-types", nil, nil, &response)
	if err != nil {
		return nil, err
	}
	engines := make([]string, 0, len(response.Items))
	for _, item := range response.Items {
		if item.Engine != "" {
			engines = append(engines, item.Engine)
		}
	}
	return engines, nil
}
