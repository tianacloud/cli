package functions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/sdk-go/fetch"
)

type sourceTransport interface {
	Do(context.Context, fetch.Request) (*fetch.Response, error)
	Close()
}
type Source struct {
	transport sourceTransport
	sourceDir string
}

func (c *Client) Source(ctx context.Context, id string) (*Source, error) {
	details, err := c.Details(ctx, id)
	if err != nil {
		return nil, err
	}
	var grant struct {
		Hostname string `json:"hostname"`
		Origin   string `json:"origin"`
		Token    string `json:"token"`
	}
	if err := c.Request(ctx, "POST", Path(id)+"/source-connection", map[string]any{}, &grant); err != nil {
		return nil, err
	}
	origin, err := url.Parse(grant.Origin)
	if err != nil {
		return nil, errors.New("invalid source connection origin")
	}
	network, err := netconfig.Policy(ctx)
	if err != nil {
		return nil, err
	}
	config := fetch.Config{Endpoint: grant.Hostname, Token: grant.Token, Port: origin.Port(), DialAddress: os.Getenv("TIANA_GATEWAY_ADDRESS"), Network: network}
	if trust := clientconfig.FromContext(ctx); trust != nil {
		config.RootCAs = trust.Roots
	}
	transport, err := fetch.NewClient(config)
	if err != nil {
		return nil, err
	}
	return &Source{transport: transport, sourceDir: strings.TrimPrefix(details.SourceDir, "/")}, nil
}

func (s *Source) Close() { s.transport.Close() }

type Entry struct {
	Name        string `json:"name"`
	NameEncoded string `json:"name_encoded"`
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	OID         string `json:"oid"`
}
type Tree struct {
	Empty      bool    `json:"empty,omitempty"`
	Commit     string  `json:"resolved_commit_oid"`
	Entries    []Entry `json:"entries"`
	NextOffset *int    `json:"next_offset"`
}
type SourceView struct {
	Commit     string  `json:"commit"`
	Root       string  `json:"root"`
	Sample     bool    `json:"needs_initial_source"`
	Entries    []Entry `json:"entries"`
	NextOffset *int    `json:"next_offset"`
}

type SourceError struct {
	Status int
	Code   string
}

func (e *SourceError) Error() string {
	if e.Status == 409 && e.Code != "NOT_A_DIRECTORY" {
		return "Git main changed; read the new commit and reconcile local edits before saving"
	}
	return fmt.Sprintf("Git source request failed: HTTP %d (%s)", e.Status, e.Code)
}

func (s *Source) request(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	r := fetch.Request{Method: method, PathQuery: target}
	if body != nil {
		r.Body = bytes.NewReader(body)
		r.BodyLength = int64(len(body))
		r.Headers = []fetch.Header{{Name: "content-type", Value: "application/json"}}
	}
	response, err := s.transport.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.Status >= 300 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		return nil, &SourceError{Status: response.Status, Code: failure.Error.Code}
	}
	return raw, nil
}

func (s *Source) Tree(ctx context.Context, commit, directory string, offset int) (Tree, error) {
	q := url.Values{"path": {directory}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
	if commit == "" {
		q.Set("ref", "refs/heads/main")
	} else {
		q.Set("commit", commit)
	}
	raw, err := s.request(ctx, "GET", "/v1/tree?"+q.Encode(), nil)
	var tree Tree
	if err == nil {
		err = json.Unmarshal(raw, &tree)
	}
	return tree, err
}

// Select pins one main-branch snapshot before resolving its function directory.
func (s *Source) Select(ctx context.Context, commit string) (SourceView, error) {
	root, err := s.Tree(ctx, commit, "", 0)
	if err != nil {
		return SourceView{}, err
	}
	sample := func() SourceView {
		directory := s.sourceDir
		return SourceView{Commit: root.Commit, Root: directory, Sample: true, Entries: []Entry{}}
	}
	if root.Empty {
		return sample(), nil
	}
	if root.Commit == "" {
		return SourceView{}, errors.New("Git response has no resolved commit")
	}
	if s.sourceDir == "" {
		return SourceView{Commit: root.Commit, Root: "", Entries: root.Entries, NextOffset: root.NextOffset}, nil
	}
	folder, err := s.Tree(ctx, root.Commit, s.sourceDir, 0)
	if err == nil {
		return SourceView{Commit: root.Commit, Root: s.sourceDir, Entries: folder.Entries, NextOffset: folder.NextOffset}, nil
	}
	var missing *SourceError
	if !errors.As(err, &missing) || missing.Status != 404 {
		return SourceView{}, err
	}
	return sample(), nil
}

func SourcePath(root, relative string) (string, error) {
	clean := path.Clean(relative)
	if path.IsAbs(relative) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("source path must remain within the function directory")
	}
	if clean == "." {
		return root, nil
	}
	return path.Join(root, clean), nil
}

func (s *Source) Read(ctx context.Context, view SourceView, relative string) ([]byte, error) {
	file, err := SourcePath(view.Root, relative)
	if err != nil {
		return nil, err
	}
	q := url.Values{"commit": {view.Commit}, "path": {file}}
	return s.request(ctx, "GET", "/v1/file?"+q.Encode(), nil)
}

func (s *Source) Save(ctx context.Context, view SourceView, files map[string]string, name string) (string, error) {
	changes := map[string]string{}
	for name, content := range files {
		file, err := SourcePath(view.Root, name)
		if err != nil {
			return "", err
		}
		changes[file] = content
	}
	var base any
	if view.Commit != "" {
		base = view.Commit
	}
	body, err := json.Marshal(map[string]any{"base_commit": base, "files": changes, "message": "Save source of " + name})
	if err != nil {
		return "", err
	}
	raw, err := s.request(ctx, "POST", "/v1/files", body)
	if err != nil {
		return "", err
	}
	var result struct {
		Commit string `json:"commit_oid"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Commit == "" {
		return "", errors.New("save receipt is incomplete; read Git main before saving again")
	}
	return result.Commit, nil
}
