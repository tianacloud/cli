// Package fileclient implements tenant or WebApp file management and direct transfers.
package fileclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Request func(context.Context, string, string, any, any) error

// Management paths are relative to the server-owned tenant/WebApp API base.
type Client struct {
	Request Request
	HTTP    *http.Client
	Origin  string
	mu      sync.Mutex
	grant   Grant
}
type Object struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag,omitempty"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}
type Page struct {
	Items  []Object `json:"items"`
	Cursor string   `json:"cursor,omitempty"`
}
type Grant struct {
	Token     string `json:"token"`
	Origin    string `json:"origin"`
	Root      string `json:"root"`
	ScopeID   string `json:"scope_id"`
	ExpiresAt int64  `json:"expires_at"`
}
type Form struct {
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"`
	ExpiresAt int64             `json:"expires_at"`
}
type Upload struct {
	ID        string  `json:"id"`
	Path      string  `json:"path"`
	Status    string  `json:"status"`
	Completed bool    `json:"completed"`
	Cancelled bool    `json:"cancelled"`
	ExpiresAt int64   `json:"expires_at"`
	Form      *Form   `json:"form,omitempty"`
	Object    *Object `json:"object,omitempty"`
}
type Prepare struct {
	RequestID   string `json:"request_id"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Overwrite   bool   `json:"overwrite"`
}
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("file request failed (%s, HTTP %d)", e.Code, e.Status)
}

func ValidatePath(path string, directory bool) error {
	if directory {
		path = strings.TrimSuffix(path, "/")
		if path == "" {
			return nil
		}
	}
	if path == "" || !utf8.ValidString(path) || strings.ContainsAny(path, "\\\x00") {
		return errors.New("invalid relative file path")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid relative file path")
		}
	}
	lower := strings.ToLower(path)
	for _, escape := range []string{"%2e", "%2f", "%5c", "%25"} {
		if strings.Contains(lower, escape) {
			return errors.New("invalid relative file path")
		}
	}
	return nil
}
func (c *Client) List(ctx context.Context, path, cursor string) (Page, error) {
	if err := ValidatePath(path, true); err != nil {
		return Page{}, err
	}
	var page Page
	err := c.Request(ctx, "GET", "/list?path="+url.QueryEscape(path)+"&cursor="+url.QueryEscape(cursor), nil, &page)
	return page, err
}
func (c *Client) Metadata(ctx context.Context, path string) (Object, error) {
	if err := ValidatePath(path, false); err != nil {
		return Object{}, err
	}
	var object Object
	err := c.Request(ctx, "GET", "/metadata?path="+url.QueryEscape(path), nil, &object)
	return object, err
}
func (c *Client) readGrant(ctx context.Context, previous string) (Grant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.grant.ExpiresAt > time.Now().Add(30*time.Second).Unix() && (previous == "" || previous != c.grant.Token) {
		return c.grant, nil
	}
	var g Grant
	if err := c.Request(ctx, "POST", "/read-grant", map[string]string{"origin": c.Origin}, &g); err != nil {
		return Grant{}, err
	}
	u, err := url.Parse(g.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(g.Root, "/") || !strings.HasSuffix(g.Root, "/") || ValidatePath(strings.Trim(g.Root, "/"), false) != nil || g.Token == "" || g.ScopeID == "" || g.ExpiresAt <= time.Now().Unix() {
		return Grant{}, errors.New("invalid file grant response")
	}
	c.grant = g
	return g, nil
}
func contentURL(g Grant, path string) string {
	u, _ := url.Parse(g.Origin)
	u.Path = g.Root + path
	q := url.Values{"_fs_scope": {g.ScopeID}}
	u.RawQuery = q.Encode()
	return u.String()
}
func (c *Client) GetURL(ctx context.Context, path string) (string, error) {
	if err := ValidatePath(path, false); err != nil {
		return "", err
	}
	g, err := c.readGrant(ctx, "")
	if err != nil {
		return "", err
	}
	return contentURL(g, path), nil
}
func (c *Client) Get(ctx context.Context, method, path, byteRange string) (*http.Response, error) {
	if err := ValidatePath(path, false); err != nil {
		return nil, err
	}
	if method != "GET" && method != "HEAD" {
		return nil, errors.New("file content supports GET and HEAD")
	}
	g, err := c.readGrant(ctx, "")
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, contentURL(g, path), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+g.Token)
		if byteRange != "" {
			req.Header.Set("Range", byteRange)
		}
		response, err := c.http().Do(req)
		if err != nil {
			return nil, errors.New("file download transport failed")
		}
		code := response.Header.Get("X-Tiana-File-Error")
		if attempt == 0 && response.StatusCode == 403 && (code == "file_grant_expired" || code == "file_grant_invalid") {
			response.Body.Close()
			g, err = c.readGrant(ctx, g.Token)
			if err != nil {
				return nil, err
			}
			continue
		}
		return response, nil
	}
	return nil, errors.New("file grant rejected")
}
func (c *Client) Prepare(ctx context.Context, p Prepare) (Upload, error) {
	if err := ValidatePath(p.Path, false); err != nil {
		return Upload{}, err
	}
	var u Upload
	err := c.Request(ctx, "POST", "/uploads", p, &u)
	return u, err
}
func (c *Client) Status(ctx context.Context, id string) (Upload, error) {
	var u Upload
	err := c.Request(ctx, "GET", "/uploads/"+url.PathEscape(id), nil, &u)
	return u, err
}
func (c *Client) Complete(ctx context.Context, id string) (Upload, error) {
	var u Upload
	err := c.Request(ctx, "POST", "/uploads/"+url.PathEscape(id)+"/complete", struct{}{}, &u)
	return u, err
}
func (c *Client) Delete(ctx context.Context, path, requestID string) error {
	if err := ValidatePath(path, false); err != nil {
		return err
	}
	var result any
	return c.Request(ctx, "DELETE", "/object?path="+url.QueryEscape(path), map[string]string{"request_id": requestID}, &result)
}

// Upload streams a multipart form without account credentials or a whole-file buffer.
func (c *Client) Upload(ctx context.Context, form Form, path string, size int64, source io.Reader) error {
	var framing bytes.Buffer
	writer := multipart.NewWriter(&framing)
	for key, value := range form.Fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": path[strings.LastIndex(path, "/")+1:]}))
	header.Set("Content-Type", form.Fields["Content-Type"])
	if _, err := writer.CreatePart(header); err != nil {
		return err
	}
	before := framing.Len()
	if err := writer.Close(); err != nil {
		return err
	}
	body := io.MultiReader(bytes.NewReader(framing.Bytes()[:before]), io.LimitReader(source, size), bytes.NewReader(framing.Bytes()[before:]))
	req, err := http.NewRequestWithContext(ctx, "POST", form.URL, body)
	if err != nil {
		return errors.New("invalid upload endpoint")
	}
	req.ContentLength = int64(framing.Len()) + size
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.http().Do(req)
	if err != nil {
		return errors.New("file upload response unknown; resume the same command to inspect it")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{Status: response.StatusCode, Code: "FILE_UPLOAD_FAILED"}
	}
	return nil
}
func (c *Client) http() *http.Client {
	client := http.Client{}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
