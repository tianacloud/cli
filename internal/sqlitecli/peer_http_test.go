package sqlitecli

import (
	"bufio"
	"net/http"
)

func readHTTPRequest(r *bufio.Reader) (*http.Request, error) { return http.ReadRequest(r) }
