package supervisor

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

const maxEmbeddedHelperSize = 256 << 20

// EmbeddedHelperLauncher owns one immutable, platform-native helper payload.
// The helper is still a separate process and speaks the same private framed
// contract; embedding changes distribution, not the language boundary.
type EmbeddedHelperLauncher struct {
	image  []byte
	digest [sha256.Size]byte
}

func NewEmbeddedHelperLauncher(image []byte, expectedSHA256 string) (*EmbeddedHelperLauncher, error) {
	if len(image) == 0 || len(image) > maxEmbeddedHelperSize {
		return nil, ErrHelperNotTrusted
	}
	expected, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(expected) != sha256.Size {
		return nil, ErrHelperNotTrusted
	}
	actual := sha256.Sum256(image)
	if subtle.ConstantTimeCompare(actual[:], expected) != 1 {
		return nil, ErrHelperNotTrusted
	}
	owned := append([]byte(nil), image...)
	return &EmbeddedHelperLauncher{image: owned, digest: actual}, nil
}

func (l *EmbeddedHelperLauncher) ContractVersion() uint16 { return HelperContractVersion }

func (l *EmbeddedHelperLauncher) Validate() error {
	if l == nil || len(l.image) == 0 || len(l.image) > maxEmbeddedHelperSize {
		return ErrHelperNotTrusted
	}
	actual := sha256.Sum256(l.image)
	if subtle.ConstantTimeCompare(actual[:], l.digest[:]) != 1 {
		return ErrHelperNotTrusted
	}
	return nil
}

func (l *EmbeddedHelperLauncher) Launch(ctx context.Context, source CredentialSource) (HelperClient, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	command, cleanup, err := embeddedHelperCommand(contextOrBackground(ctx), l.image)
	if err != nil {
		return nil, fmt.Errorf("%w: prepare embedded helper", ErrHelperProtocol)
	}
	return launchHelperCommand(ctx, command, source, cleanup)
}

var _ HelperLauncher = (*EmbeddedHelperLauncher)(nil)
