//go:build !darwin && !linux && !windows

package supervisor

import "context"

func promptCredential(context.Context) (*SecretToken, error) { return nil, missingCredentialError() }
