package shellwords

import (
	"errors"
	"strings"
	"unicode"
)

func Fields(input string) ([]string, error) {
	var fields []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	haveField := false

	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			haveField = true
			escaped = false
			continue
		}

		switch {
		case r == '\\' && !inSingle:
			escaped = true
			haveField = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			haveField = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			haveField = true
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			if haveField {
				fields = append(fields, current.String())
				current.Reset()
				haveField = false
			}
		default:
			current.WriteRune(r)
			haveField = true
		}
	}

	if escaped {
		return nil, errors.New("trailing escape")
	}
	if inSingle || inDouble {
		return nil, errors.New("unterminated quote")
	}
	if haveField {
		fields = append(fields, current.String())
	}
	return fields, nil
}
