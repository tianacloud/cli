package sqlitecli

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const MaxStatements = 10000

type Statement struct {
	SQL         string
	Transaction bool
}

// Split validates lexical boundaries and resource limits, never SQL grammar.
// All statements are bounded before any are executed; syntax and schema errors
// are determined by the server and may follow earlier autocommit writes.
func Split(sql string) ([]Statement, *Error) { parts, _, e := splitInput(sql); return parts, e }
func splitInput(sql string) ([]Statement, bool, *Error) {
	if len(sql) > MaxBytes || !utf8.ValidString(sql) || strings.ContainsRune(sql, 0) {
		return nil, false, inputError("SQL must be UTF-8 without NUL and at most 8 MiB")
	}
	var parts []Statement
	start := -1
	depth := 0
	state := completeInvalid
	terminated := false
	policy := statementPolicy{}
	appendStatement := func(end int) *Error {
		if start < 0 {
			return nil
		}
		source := sql[start:end]
		encoded, e := json.Marshal(executeRequest(source, true))
		if e != nil || len(encoded) > MaxBytes-8192 {
			return inputError("encoded SQL request exceeds limit")
		}
		parts = append(parts, Statement{SQL: source, Transaction: policy.transaction})
		if len(parts) > MaxStatements {
			return inputError("script exceeds 10000 statements")
		}
		start = -1
		policy = statementPolicy{}
		return nil
	}
	for i := 0; i < len(sql); {
		t, e := scanToken(sql, i)
		if e != nil {
			return nil, false, e
		}
		i = t.end
		if start < 0 && t.kind != tokenTrivia && t.kind != tokenSemi {
			start = t.start
		}
		word := keyword(sql, t)
		if start >= 0 {
			if e := policy.observe(t.kind, word); e != nil {
				return nil, false, e
			}
		}
		tokenDepth := depth
		switch t.kind {
		case tokenLeft:
			depth++
			if depth > 128 {
				return nil, false, inputError("SQL nesting exceeds 128 levels")
			}
		case tokenRight:
			depth--
			if depth < 0 {
				return nil, false, inputError("unbalanced SQL parentheses")
			}
		}
		state = advanceCompletion(state, t.kind, word, tokenDepth)
		if t.kind == tokenTrivia {
			continue
		}
		terminated = false
		if t.kind == tokenSemi && depth == 0 && state == completeStart {
			if e := appendStatement(t.end); e != nil {
				return nil, false, e
			}
			terminated = true
		}
	}
	if depth != 0 {
		return nil, false, incompleteInput("unbalanced SQL parentheses")
	}
	if state == completeTrigger || state == completeTriggerSemi {
		return nil, false, incompleteInput("incomplete CREATE TRIGGER")
	}
	// Final SQL without ';' is accepted for files/EOF; an unfinished trigger is
	// not. A trigger ending in END may omit the final top-level semicolon at EOF.
	if e := appendStatement(len(sql)); e != nil {
		return nil, false, e
	}
	return parts, terminated, nil
}

func skipTrivia(s string, i int) int {
	for i < len(s) {
		t, e := scanToken(s, i)
		if e != nil || t.kind != tokenTrivia && t.kind != tokenSemi {
			return i
		}
		i = t.end
	}
	return i
}

func validateLexical(s string) *Error {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return inputError("SQL must be UTF-8 without NUL")
	}
	depth := 0
	for i := 0; i < len(s); {
		t, e := scanToken(s, i)
		if e != nil {
			return e
		}
		i = t.end
		switch t.kind {
		case tokenLeft:
			depth++
			if depth > 128 {
				return inputError("SQL nesting exceeds 128 levels")
			}
		case tokenRight:
			depth--
			if depth < 0 {
				return inputError("unbalanced SQL parentheses")
			}
		}
	}
	if depth != 0 {
		return incompleteInput("unbalanced SQL parentheses")
	}
	return nil
}
func Ready(sql string) bool { ready, e := readiness(sql); return ready && e == nil }
func readiness(sql string) (bool, *Error) {
	_, terminated, e := splitInput(sql)
	if e != nil && e.incomplete {
		return false, nil
	}
	return terminated, e
}
func ReadScript(path string) (string, *Error) {
	b, e := ReadFile(path, MaxBytes)
	if e != nil {
		return "", inputError("SQL file unreadable or exceeds 8 MiB")
	}
	return string(b), nil
}
