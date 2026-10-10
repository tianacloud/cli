package sqlitecli

import "strings"

type tokenKind uint8

const (
	tokenOther tokenKind = iota
	tokenTrivia
	tokenWord
	tokenSemi
	tokenLeft
	tokenRight
)

type sqlToken struct {
	start, end int
	kind       tokenKind
}

// scanToken keeps quoted names, comments and native bind suffixes indivisible.
// Its byte offsets always refer to the original UTF-8 input; no grammar copy is
// made. Literal values and identifier names are never treated as SQL keywords.
func scanToken(s string, i int) (sqlToken, *Error) {
	t := sqlToken{start: i, end: i + 1, kind: tokenOther}
	switch {
	case sqlTokenSpace(s[i]):
		t.kind = tokenTrivia
		for t.end < len(s) && sqlTokenSpace(s[t.end]) {
			t.end++
		}
		return t, nil
	case strings.HasPrefix(s[i:], "\xef\xbb\xbf"):
		t.kind = tokenTrivia
		t.end = i + 3
		return t, nil
	case strings.HasPrefix(s[i:], "--"):
		t.kind = tokenTrivia
		t.end = len(s)
		if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
			t.end = i + n + 1
		}
		return t, nil
	case strings.HasPrefix(s[i:], "/*"):
		t.kind = tokenTrivia
		if n := strings.Index(s[i+2:], "*/"); n >= 0 {
			t.end = i + n + 4
			return t, nil
		}
		return t, incompleteInput("unterminated SQL comment")
	}
	b := s[i]
	if b == '\'' || b == '"' || b == '`' || b == '[' {
		quote := b
		if quote == '[' {
			quote = ']'
		}
		for j := i + 1; j < len(s); j++ {
			if s[j] != quote {
				continue
			}
			if b != '[' && j+1 < len(s) && s[j+1] == quote {
				j++
				continue
			}
			t.end = j + 1
			return t, nil
		}
		return t, incompleteInput("unterminated SQL quote")
	}
	end, e := nativeTokenEnd(s, i)
	if e != nil {
		return t, e
	}
	t.end = end
	switch b {
	case ';':
		t.kind = tokenSemi
	case '(':
		t.kind = tokenLeft
	case ')':
		t.kind = tokenRight
	default:
		if identifierByte(b) && !decimalDigit(b) && b != '$' && !((b == 'x' || b == 'X') && tokenByte(s, i+1) == '\'') {
			t.kind = tokenWord
		}
	}
	return t, nil
}

func incompleteInput(message string) *Error { e := inputError(message); e.incomplete = true; return e }

func keyword(s string, t sqlToken) string {
	if t.kind != tokenWord || t.end-t.start > 9 {
		return ""
	}
	return strings.ToUpper(s[t.start:t.end])
}
