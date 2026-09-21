package authclient

import (
	"testing"
	"time"
)

func TestResolveExpiresAt(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		value   string
		want    int64
		wantErr bool
	}{
		{name: "never", value: "never", want: InstanceTokenNoExpiry},
		{name: "seconds", value: "2s", want: 1789041602},
		{name: "minutes", value: "30m", want: 1789043400},
		{name: "hours", value: "24h", want: 1789128000},
		{name: "days", value: "7d", want: 1789646400},
		{name: "weeks", value: "2w", want: 1790251200},
		{name: "zero", value: "0d", wantErr: true},
		{name: "negative", value: "-1d", wantErr: true},
		{name: "unknown unit", value: "5y", wantErr: true},
		{name: "bare number", value: "5", wantErr: true},
		{name: "empty", value: "", wantErr: true},
		{name: "huge normalizes to never", value: "99999999999999999999d", want: InstanceTokenNoExpiry},
		{name: "large seconds remain representable", value: "999999999999h", want: now.Unix() + 999999999999*3600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveExpiresAt(now, test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ResolveExpiresAt(%q)=%d, want error", test.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveExpiresAt(%q) error: %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("ResolveExpiresAt(%q)=%d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestFormatExpiration(t *testing.T) {
	if got := FormatExpiration(InstanceTokenNoExpiry); got != "never" {
		t.Fatalf("FormatExpiration(no-expiry)=%q", got)
	}
	if got := FormatExpiration(1789646400); got != "2026-09-17T12:00:00.000Z" {
		t.Fatalf("FormatExpiration(finite)=%q", got)
	}
}
