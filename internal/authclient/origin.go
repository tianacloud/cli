package authclient

import (
	"context"
	"errors"
	"os"

	"github.com/tianacloud/cli/internal/clientconfig"
)

// ResolveOrigin selects the inherited management origin or the online default.
func ResolveOrigin(_ context.Context) (string, error) {
	if origin := DefaultOrigin(); origin != "" {
		return origin, nil
	}
	if _, set := os.LookupEnv("TIANA_API_ORIGIN"); set {
		return "", errors.New("TIANA_API_ORIGIN is empty; unset it to use online or set a management origin")
	}
	return clientconfig.DefaultManagementOrigin, nil
}
