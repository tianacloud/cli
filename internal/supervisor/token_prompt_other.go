//go:build !darwin && !linux

package supervisor

import "context"

func promptCredential(context.Context) (*SecretToken, error) { return nil, missingCredentialError() }
