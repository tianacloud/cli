package sqlitecli

// This completion state machine follows SQLite's CREATE [TEMP] TRIGGER and
// EXPLAIN handling, without accepting/rejecting SQL grammar. Parentheses are
// tracked separately: a semicolon in opaque module arguments is not a boundary.
const (
	completeInvalid uint8 = iota
	completeStart
	completeNormal
	completeExplain
	completeCreate
	completeTrigger
	completeTriggerSemi
	completeTriggerEnd
)

var completionTransitions = [8][8]uint8{
	{1, 0, 2, 3, 4, 2, 2, 2},
	{1, 1, 2, 3, 4, 2, 2, 2},
	{1, 2, 2, 2, 2, 2, 2, 2},
	{1, 3, 3, 2, 4, 2, 2, 2},
	{1, 4, 2, 2, 2, 4, 5, 2},
	{6, 5, 5, 5, 5, 5, 5, 5},
	{6, 6, 5, 5, 5, 5, 5, 7},
	{1, 7, 5, 5, 5, 5, 5, 5},
}

func advanceCompletion(state uint8, kind tokenKind, word string, depth int) uint8 {
	token := 2
	if kind == tokenTrivia {
		token = 1
	} else if depth == 0 {
		switch {
		case kind == tokenSemi:
			token = 0
		case word == "EXPLAIN":
			token = 3
		case word == "CREATE":
			token = 4
		case word == "TEMP" || word == "TEMPORARY":
			token = 5
		case word == "TRIGGER":
			token = 6
		case word == "END":
			token = 7
		}
	}
	return completionTransitions[state][token]
}

// Policy is independent of grammar. Preserve the existing CLI restrictions,
// including EXPLAIN prefixes; server policies must still defend direct clients.
type statementPolicy struct {
	prefix      uint8
	transaction bool
}

func (p *statementPolicy) observe(kind tokenKind, word string) *Error {
	if kind == tokenTrivia || p.prefix == 4 {
		return nil
	}
	switch p.prefix {
	case 0:
		if word == "EXPLAIN" {
			p.prefix = 1
			return nil
		}
		switch word {
		case "BEGIN", "COMMIT", "END", "ROLLBACK", "SAVEPOINT", "RELEASE":
			p.transaction = true
		}
	case 1:
		if word == "QUERY" {
			p.prefix = 2
			return nil
		}
	case 2:
		if word == "PLAN" {
			p.prefix = 3
			return nil
		}
	}
	p.prefix = 4
	switch word {
	case "ATTACH", "DETACH", "VACUUM":
		return inputError("SQL command is not supported by the CLI")
	}
	return nil
}
