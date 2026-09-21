package sqlitecli

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	parser "github.com/rqlite/sql"
)

const MaxStatements = 10000

type Statement struct {
	SQL         string
	Transaction bool
}

// The parser validates a conservative SQLite subset. Original byte slices are
// sent unchanged; AST.String must never rewrite SQL. Atomic mode stays disabled
// until a grammar matching the App's SQLite parser passes differential tests.
func Split(sql string) ([]Statement, *Error) {
	if len(sql) > MaxBytes || !utf8.ValidString(sql) || strings.ContainsRune(sql, 0) {
		return nil, inputError("SQL must be UTF-8 without NUL and at most 8 MiB")
	}
	if e := validateLexical(sql); e != nil {
		return nil, e
	}
	var statements []Statement
	offset := 0
	for offset < len(sql) {
		offset = skipTrivia(sql, offset)
		if offset == len(sql) {
			break
		}
		r := strings.NewReader(sql[offset:])
		stmt, err := parser.NewParser(r).ParseStatement()
		if err != nil {
			e := inputError("invalid, incomplete or unsupported SQLite syntax")
			var parseErr *parser.Error
			e.incomplete = errors.As(err, &parseErr) && strings.HasSuffix(parseErr.Msg, ", found 'EOF'")
			return nil, e
		}
		end := len(sql) - r.Len()
		if end <= offset {
			return nil, inputError("cannot determine SQL boundary")
		}
		tx := false
		switch stmt.(type) {
		case *parser.BeginStatement, *parser.CommitStatement, *parser.RollbackStatement, *parser.SavepointStatement, *parser.ReleaseStatement:
			tx = true
		}
		statements = append(statements, Statement{SQL: sql[offset:end], Transaction: tx})
		// Reserve room for the maximum baton and request envelope. Reject every
		// oversized encoded statement before executing any part of a script.
		encoded, err := json.Marshal(executeRequest(sql[offset:end], true))
		if err != nil || len(encoded) > MaxBytes-8192 {
			return nil, inputError("encoded SQL request exceeds limit")
		}
		if len(statements) > MaxStatements {
			return nil, inputError("script exceeds 10000 statements")
		}
		offset = end
	}
	return statements, nil
}

func skipTrivia(s string, i int) int {
	for i < len(s) {
		switch {
		case strings.ContainsRune(" \t\r\n\f;", rune(s[i])):
			i++
		case strings.HasPrefix(s[i:], "--"):
			if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
				i += n + 1
			} else {
				return len(s)
			}
		case strings.HasPrefix(s[i:], "/*"):
			if n := strings.Index(s[i+2:], "*/"); n >= 0 {
				i += n + 4
			} else {
				return len(s)
			}
		default:
			return i
		}
	}
	return i
}

// This lexical guard closes known scanner differences (mixed identifier quotes
// and non-SQLite Unicode whitespace) and bounds nesting before invoking the
// grammar parser. It does not infer statement or transaction boundaries.
func validateLexical(s string) *Error {
	depth := 0
	for i := 0; i < len(s); {
		ch := s[i]
		if strings.HasPrefix(s[i:], "--") {
			n := strings.IndexByte(s[i:], '\n')
			if n < 0 {
				break
			}
			i += n + 1
			continue
		}
		if strings.HasPrefix(s[i:], "/*") {
			n := strings.Index(s[i+2:], "*/")
			if n < 0 {
				e := inputError("unterminated SQL comment")
				e.incomplete = true
				return e
			}
			i += n + 4
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			quote := ch
			i++
			closed := false
			for i < len(s) {
				if s[i] == quote {
					if i+1 < len(s) && s[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				if (quote == '"' && s[i] == '`') || (quote == '`' && s[i] == '"') {
					return inputError("mixed identifier quotes are unsupported by the current parser")
				}
				i++
			}
			if !closed {
				e := inputError("unterminated SQL quote")
				e.incomplete = true
				return e
			}
			continue
		}
		if ch == '(' {
			depth++
			if depth > 128 {
				return inputError("SQL nesting exceeds 128 levels")
			}
		}
		if ch == ')' {
			depth--
			if depth < 0 {
				return inputError("unbalanced SQL parentheses")
			}
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) && !strings.ContainsRune(" \t\r\n\f", r) {
			return inputError("unsupported SQL whitespace")
		}
		i += n
	}
	if depth != 0 {
		e := inputError("unbalanced SQL parentheses")
		e.incomplete = true
		return e
	}
	return nil
}

// Ready returns false for incomplete multiline grammar, including trigger
// bodies. EOF is handled with Split so an incomplete final buffer is an error.
func Ready(sql string) bool {
	ready, err := readiness(sql)
	return ready && err == nil
}

func readiness(sql string) (bool, *Error) {
	if e := validateLexical(sql); e != nil {
		if e.incomplete {
			return false, nil
		}
		return false, e
	}
	scanner := parser.NewScanner(strings.NewReader(sql))
	last := parser.EOF
	for {
		_, tok, _ := scanner.Scan()
		if tok == parser.EOF {
			break
		}
		if tok != parser.COMMENT {
			last = tok
		}
	}
	if last != parser.SEMI {
		return false, nil
	}
	_, e := Split(sql)
	if e != nil && e.incomplete {
		return false, nil
	}
	return true, e
}

func ReadScript(path string) (string, *Error) {
	b, err := ReadFile(path, MaxBytes)
	if err != nil {
		return "", inputError("SQL file unreadable or exceeds 8 MiB")
	}
	return string(b), nil
}
