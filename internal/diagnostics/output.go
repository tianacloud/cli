// Package diagnostics carries the optional CLI request-ID output destination.
package diagnostics

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type key struct{}
type output struct {
	mu sync.Mutex
	w  io.Writer
}

func WithWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, key{}, &output{w: w})
}

func (out *output) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.w.Write(p)
}

func Writer(ctx context.Context) io.Writer {
	if out, ok := ctx.Value(key{}).(*output); ok {
		return out
	}
	return io.Discard
}

func Write(ctx context.Context, component, requestID string) {
	if out, ok := ctx.Value(key{}).(*output); ok && requestID != "" {
		_, _ = fmt.Fprintf(out, "Request ID: %s (%s)\n", requestID, component)
	}
}
