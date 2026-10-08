package main

import (
	"strconv"
	"strings"
	"unicode"
)

// safeDisplay renders untrusted management metadata as visible text. Keep
// original values for API calls, persisted state and deliberately delivered data.
func safeDisplay(value string) string {
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			quoted := strconv.QuoteRuneToASCII(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
