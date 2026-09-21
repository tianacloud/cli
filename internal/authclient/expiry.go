package authclient

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const canonicalMillisecondLayout = "2006-01-02T15:04:05.000Z"

var expirationPattern = regexp.MustCompile(`^([0-9]+)([smhdw])$`)

var expirationUnits = map[byte]uint64{
	's': 1,
	'm': 60,
	'h': 60 * 60,
	'd': 24 * 60 * 60,
	'w': 7 * 24 * 60 * 60,
}

// ResolveExpiresAt converts a CLI duration to Unix seconds or -1 for no expiry.
func ResolveExpiresAt(now time.Time, value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "never" {
		return InstanceTokenNoExpiry, nil
	}
	match := expirationPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("expiration must be \"never\" or a positive number followed by s, m, h, d, or w")
	}
	count, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		// A positive value too large for uint64 still represents an
		// unbounded lifetime.
		return InstanceTokenNoExpiry, nil
	}
	if count == 0 {
		return 0, fmt.Errorf("expiration must be a positive duration")
	}
	unit := expirationUnits[match[2][0]]
	if count > uint64(1<<63-1)/unit {
		return InstanceTokenNoExpiry, nil
	}
	seconds := count * unit
	if now.Unix() > (1<<63-1)-int64(seconds) {
		return InstanceTokenNoExpiry, nil
	}
	return now.Unix() + int64(seconds), nil
}

// FormatExpiration renders an absolute expires_at for display.
func FormatExpiration(value int64) string {
	if value == InstanceTokenNoExpiry {
		return "never"
	}
	return time.Unix(value, 0).UTC().Format(canonicalMillisecondLayout)
}
