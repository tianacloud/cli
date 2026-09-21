package authclient

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestTokenWireAndLocalRecordsCarryNumericExpiry(t *testing.T) {
	for _, expires := range []int64{-1, 1800000000} {
		payload := []byte(`{"expires_at":` + strconv.FormatInt(expires, 10) + `}`)
		for _, record := range []any{&CreateTokenRequest{}, &TokenResult{}, &InstanceTokenCredential{}, &PendingCommand{}} {
			if err := json.Unmarshal(payload, record); err != nil {
				t.Fatalf("%T numeric expiry: %v", record, err)
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["expires_at"]) != strconv.FormatInt(expires, 10) {
				t.Fatalf("%T expiry encoded as %s", record, fields["expires_at"])
			}
		}
	}
}

func TestDurationProducesUnixSeconds(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 987000000, time.UTC)
	expiry, err := ResolveExpiresAt(now, "2s")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(expiry)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != strconv.FormatInt(now.Unix()+2, 10) {
		t.Fatalf("expiry is %s; want Unix seconds", encoded)
	}
}
