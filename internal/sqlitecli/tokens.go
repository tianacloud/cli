package sqlitecli

// nativeTokenEnd checks token legality using SQLite tokenizer rules; it does
// not validate SQL grammar. Quotes/comments are handled by scanToken. Illegal
// tokens fail full-input preflight before any script statements execute.
func nativeTokenEnd(s string, start int) (int, *Error) {
	b := tokenByte(s, start)
	i := start + 1
	invalid := func() (int, *Error) {
		return 0, inputError("invalid SQLite token")
	}
	if (b == 'x' || b == 'X') && tokenByte(s, i) == '\'' {
		i++
		begin := i
		for hexDigit(tokenByte(s, i)) {
			i++
		}
		if tokenByte(s, i) != '\'' || (i-begin)%2 != 0 {
			return invalid()
		}
		return i + 1, nil
	}
	if decimalDigit(b) || b == '.' && decimalDigit(tokenByte(s, i)) {
		i = start
		if b == '0' && (tokenByte(s, start+1) == 'x' || tokenByte(s, start+1) == 'X') && hexDigit(tokenByte(s, start+2)) {
			i = start + 3
			for hexDigit(tokenByte(s, i)) || tokenByte(s, i) == '_' {
				i++
			}
		} else {
			for decimalDigit(tokenByte(s, i)) || tokenByte(s, i) == '_' {
				i++
			}
			if tokenByte(s, i) == '.' {
				i++
				for decimalDigit(tokenByte(s, i)) || tokenByte(s, i) == '_' {
					i++
				}
			}
			if tokenByte(s, i) == 'e' || tokenByte(s, i) == 'E' {
				next := i + 1
				if tokenByte(s, next) == '+' || tokenByte(s, next) == '-' {
					next++
				}
				if decimalDigit(tokenByte(s, next)) {
					i = next + 1
					for decimalDigit(tokenByte(s, i)) || tokenByte(s, i) == '_' {
						i++
					}
				}
			}
		}
		if identifierByte(tokenByte(s, i)) {
			return invalid()
		}
		return i, nil
	}
	switch b {
	case ' ', '\t', '\r', '\n', '\f', '(', ')', ';', '+', '-', '*', '/', '%', '=', '<', '>', '|', ',', '&', '~', '.':
		return i, nil
	case '!':
		if tokenByte(s, i) != '=' {
			return invalid()
		}
		return i + 1, nil
	case '?':
		for decimalDigit(tokenByte(s, i)) {
			i++
		}
		return i, nil
	case '$', '@', ':', '#':
		nameBytes := 0
		for i < len(s) {
			switch {
			case identifierByte(s[i]):
				nameBytes++
				i++
			case s[i] == ':' && tokenByte(s, i+1) == ':':
				i += 2
			case s[i] == '(' && nameBytes > 0:
				i++
				for i < len(s) && s[i] != ')' && !sqlTokenSpace(s[i]) {
					i++
				}
				if tokenByte(s, i) != ')' {
					return invalid()
				}
				return i + 1, nil
			default:
				if nameBytes == 0 {
					return invalid()
				}
				return i, nil
			}
		}
		if nameBytes == 0 {
			return invalid()
		}
		return i, nil
	default:
		if !identifierByte(b) {
			return invalid()
		}
		for identifierByte(tokenByte(s, i)) {
			i++
		}
		return i, nil
	}
}

func tokenByte(s string, i int) byte {
	if i >= len(s) {
		return 0
	}
	return s[i]
}

func decimalDigit(b byte) bool { return b >= '0' && b <= '9' }

func hexDigit(b byte) bool {
	return decimalDigit(b) || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func sqlTokenSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f'
}

func identifierByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || decimalDigit(b) || b == '_' || b == '$' || b >= 0x80
}
