package supervisor

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
)

// Retain original bytes, including same-read body bytes. Nothing is sent to
// Gateway until the classifier and the CONNECT 200 barrier have both passed.
func readBuiltinPrefix(r io.Reader) ([]byte, string, error) {
	prefix := make([]byte, 0, SQLDHeaderLimit+8192)
	var chunk [8192]byte
	defer clear(chunk[:])
	fail := func() ([]byte, string, error) { clear(prefix); return nil, "", ErrUnsupportedProfile }
	for {
		n, err := r.Read(chunk[:])
		prefix = append(prefix, chunk[:n]...)
		if end := bytes.Index(prefix, []byte("\r\n\r\n")); end >= 0 {
			end += 4
			if end > SQLDHeaderLimit || len(prefix) > SQLDPre200Limit {
				return fail()
			}
			profile, e := classifyBuiltinHeader(prefix[:end])
			if e != nil {
				return fail()
			}
			return prefix, profile, nil
		}
		if err != nil || n == 0 || len(prefix) >= SQLDHeaderLimit {
			return fail()
		}
	}
}

func classifyBuiltinHeader(raw []byte) (string, error) {
	bad := func() (string, error) { return "", ErrUnsupportedProfile }
	lines := strings.Split(string(raw), "\r\n")
	if len(lines) < 4 || len(lines)-3 > SQLDFieldLimit {
		return bad()
	}
	request := strings.Split(lines[0], " ")
	if len(request) != 3 || request[2] != "HTTP/1.1" || !builtinHTTPToken(request[0]) || request[1] == "" {
		return bad()
	}
	for _, b := range []byte(request[1]) {
		if b <= 32 || b == 127 {
			return bad()
		}
	}
	fields := make(map[string][]string)
	for _, line := range lines[1 : len(lines)-2] {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !builtinHTTPToken(name) {
			return bad()
		}
		for _, b := range []byte(value) {
			if b < 32 && b != '\t' || b == 127 {
				return bad()
			}
		}
		name = strings.ToLower(name)
		fields[name] = append(fields[name], strings.Trim(value, " \t"))
	}
	single := func(key string) string {
		if len(fields[key]) != 1 {
			return ""
		}
		return fields[key][0]
	}
	if single("host") == "" {
		return bad()
	}
	if request[0] == "POST" {
		switch request[1] {
		case "/v2/pipeline", "/v3/pipeline", "/v3/cursor":
			return ProfileHranaHTTP, nil
		}
	}
	if request[0] == "GET" && request[1] == "/" {
		has := func(value, want string) bool {
			for _, v := range strings.Split(value, ",") {
				if strings.EqualFold(strings.Trim(v, " \t"), want) {
					return true
				}
			}
			return false
		}
		key := single("sec-websocket-key")
		decoded, err := base64.StdEncoding.Strict().DecodeString(key)
		if has(single("connection"), "upgrade") && has(single("upgrade"), "websocket") && single("sec-websocket-version") == "13" && err == nil && len(decoded) == 16 && base64.StdEncoding.EncodeToString(decoded) == key {
			return ProfileHranaWebSocket, nil
		}
	}
	return bad()
}
func builtinHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for _, b := range []byte(s) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b))) {
			return false
		}
	}
	return true
}
