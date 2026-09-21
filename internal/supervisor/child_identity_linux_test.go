//go:build linux

package supervisor

import (
	"strings"
	"testing"
)

func TestProcStartTimeParserHandlesSpacesAndParentheses(t *testing.T) {
	fields := make([]string, 20)
	fields[0] = "S"
	for i := 1; i < len(fields); i++ {
		fields[i] = "1"
	}
	fields[19] = "4242"
	value, err := parseProcStartTime([]byte("123 (name with ) and spaces) " + strings.Join(fields, " ")))
	if err != nil || value != 4242 {
		t.Fatalf("value=%d err=%v", value, err)
	}
}
