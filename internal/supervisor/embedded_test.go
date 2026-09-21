package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestEmbeddedHelperRequiresExactBuildDigestAndOwnsBytes(t *testing.T) {
	image := []byte("immutable helper payload")
	digest := sha256.Sum256(image)
	launcher, err := NewEmbeddedHelperLauncher(image, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("NewEmbeddedHelperLauncher: %v", err)
	}
	image[0] ^= 0xff
	if err := launcher.Validate(); err != nil {
		t.Fatalf("launcher retained caller-owned bytes: %v", err)
	}
	launcher.image[0] ^= 0xff
	if err := launcher.Validate(); !errors.Is(err, ErrHelperNotTrusted) {
		t.Fatalf("tampered embedded image err=%v", err)
	}
}

func TestEmbeddedHelperRejectsInvalidInputs(t *testing.T) {
	image := []byte("helper")
	digest := sha256.Sum256(image)
	for _, test := range []struct {
		image  []byte
		digest string
	}{
		{nil, hex.EncodeToString(digest[:])},
		{image, "not-a-digest"},
		{image, string(make([]byte, sha256.Size*2))},
		{image, hex.EncodeToString(make([]byte, sha256.Size))},
	} {
		if _, err := NewEmbeddedHelperLauncher(test.image, test.digest); !errors.Is(err, ErrHelperNotTrusted) {
			t.Fatalf("NewEmbeddedHelperLauncher(%d, %q) err=%v", len(test.image), test.digest, err)
		}
	}
}
