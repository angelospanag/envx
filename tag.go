package envx

import (
	"fmt"
	"strings"
)

// fieldOpts holds the parsed `env` struct tag.
type fieldOpts struct {
	name       string
	def        string
	hasDefault bool
	required   bool
	notEmpty   bool
	separator  string
}

// parseFieldTag parses the `env` tag. An unrecognised option is an error, not
// something to skip: options are comma-separated, so ignoring them would turn
// `default=a,b` into "a" with the rest dropped, and swallow a miscased
// `notempty`. Single-quote a value to include a comma.
func parseFieldTag(tag string) (fieldOpts, error) {
	o := fieldOpts{separator: ","}
	name, rest, _ := strings.Cut(tag, ",")
	o.name = strings.TrimSpace(name)

	for rest != "" {
		var part string
		part, rest = nextTagOption(rest)
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, hasVal := strings.Cut(part, "=")
		v, err := unquoteTagValue(v)
		if err != nil {
			return o, err
		}
		switch strings.TrimSpace(k) {
		case "default":
			o.def, o.hasDefault = v, hasVal
		case "required":
			o.required = true
		case "notEmpty":
			o.notEmpty = true
		case "separator":
			// Empty would split into single characters, and is also what
			// `separator=,` yields since the comma is the option delimiter.
			if v != "" {
				o.separator = v
			}
		default:
			return o, fmt.Errorf(
				"%w: unknown option %q (quote a value containing a comma, as default='a,b')",
				ErrInvalidTag, strings.TrimSpace(k),
			)
		}
	}
	return o, nil
}

// nextTagOption splits off the next option, honouring single quotes so a
// quoted value may contain commas.
func nextTagOption(s string) (part, rest string) {
	quoted := false
	for i := range len(s) {
		switch s[i] {
		case '\'':
			quoted = !quoted
		case ',':
			if !quoted {
				return s[:i], s[i+1:]
			}
		}
	}
	return s, ""
}

func unquoteTagValue(v string) (string, error) {
	if !strings.HasPrefix(v, "'") {
		// Trim, so `default= 8080` parses. Quote to keep whitespace.
		return strings.TrimSpace(v), nil
	}
	if len(v) < 2 || !strings.HasSuffix(v, "'") {
		return "", fmt.Errorf("%w: unbalanced quote in %q", ErrInvalidTag, v)
	}
	return v[1 : len(v)-1], nil
}

// screamingSnake derives a variable name from a field name, keeping acronym
// runs together: DBPassword becomes DB_PASSWORD, not D_B_PASSWORD.
func screamingSnake(s string) string {
	r := []rune(s)
	var b strings.Builder
	b.Grow(len(r) + 4)
	for i, c := range r {
		if i > 0 && isUpper(c) {
			prev := r[i-1]
			nextLower := i+1 < len(r) && isLower(r[i+1])
			if isLower(prev) || isDigit(prev) || (isUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(toUpper(c))
	}
	return b.String()
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }

func isLower(r rune) bool { return r >= 'a' && r <= 'z' }

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func toUpper(r rune) rune {
	if isLower(r) {
		return r - 'a' + 'A'
	}
	return r
}
