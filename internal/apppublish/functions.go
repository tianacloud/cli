package apppublish

import (
	"context"
	"encoding/json"
	"net/url"
)

type WebFunctions struct {
	WebID               string   `json:"web_id"`
	FunctionInstanceIDs []string `json:"function_instance_ids"`
	ProjectRevision     uint64   `json:"project_revision"`
	CanManage           bool     `json:"can_manage"`
}
type WebFunctionsUpdate struct {
	FunctionInstanceIDs []string `json:"function_instance_ids"`
	ExpectedRevision    uint64   `json:"expected_revision"`
}

func (r Runner) Functions(ctx context.Context, id string, update *WebFunctionsUpdate) (WebFunctions, *Error) {
	method := "GET"
	var body any
	if update != nil {
		method, body = "PUT", update
	}
	res, e := r.boundRequest(ctx, method, "/api/v1/web-projects/"+url.PathEscape(id)+"/functions", body)
	if e != nil {
		return WebFunctions{}, e
	}
	var result WebFunctions
	if json.Unmarshal(res.Body, &result) != nil || result.WebID != id {
		return result, invalidWebResponse(res.RequestID)
	}
	return result, nil
}
