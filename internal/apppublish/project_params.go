package apppublish

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

// Product metadata is supplied to MGR; archives contain only server YAML and data.
type WebProjectParams struct {
	Entry              string `json:"entry,omitempty"`
	DatabaseInstanceID string `json:"database_instance_id,omitempty"`
	GitInstanceID      string `json:"git_instance_id,omitempty"`
}

func ValidEntry(s string) bool {
	if !utf8.ValidString(s) || len(s) > 512 || !fs.ValidPath(s) || s == "." || strings.ContainsAny(s, "\\\x00\r\n?#%:") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return path.Ext(s) == ".js" || path.Ext(s) == ".mjs"
}
func (p WebProjectParams) Validate() error {
	if p.Entry != "" && !ValidEntry(p.Entry) {
		return errors.New("entry must be a safe relative .js/.mjs path")
	}
	if p.Entry == "" && p.DatabaseInstanceID != "" {
		return errors.New("database binding requires a Site entry")
	}
	for _, id := range []string{p.DatabaseInstanceID, p.GitInstanceID} {
		if id != "" && !validInstanceID(id) {
			return errors.New("invalid bound instance ID")
		}
	}
	return nil
}
func validInstanceID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
