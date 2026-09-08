package envx

import (
	"fmt"
	"strings"
)

// parseDotEnv parses .env file contents into a map. It never touches
// os.Environ.
//
// Supported syntax:
//
//	KEY=value            unquoted; trailing " #comment" stripped
//	KEY="value"          double-quoted; \n \r \t \\ \" \$ escapes, may span lines
//	KEY='value'          single-quoted; fully literal, may span lines
//	export KEY=value     the export prefix is ignored
//	KEY=                 present, empty string (see the set-but-empty rule)
//
// Unquoted and double-quoted values expand ${VAR}, $VAR and ${VAR:-default}
// against keys defined earlier in the same file. Expansion never reaches into
// other layers or the process environment, which keeps each file readable on
// its own. Write \$ or use single quotes to disable it.
func parseDotEnv(data string) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}

		rawKey, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=value, got %q", i+1, line)
		}
		key := strings.TrimSpace(rawKey)
		if err := checkKey(key); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		// stripComment needs the leading whitespace, so keep both forms.
		trimmed := strings.TrimLeft(rest, " \t")

		var val string
		switch {
		case strings.HasPrefix(trimmed, `'`), strings.HasPrefix(trimmed, `"`):
			quote := trimmed[0]
			body, end, err := readQuoted(lines, i, trimmed[1:], quote)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			i = end
			if quote == '"' {
				val = unescapeDouble(expandVars(body, out))
			} else {
				val = body
			}
		default:
			val = expandVars(strings.TrimSpace(stripComment(rest)), out)
			// Unquoted values get no escape processing beyond \$, which has to
			// work here or there would be no way to write a literal $ outside
			// single quotes.
			val = strings.ReplaceAll(val, `\$`, "$")
		}
		out[key] = val
	}
	return out, nil
}

// readQuoted consumes a quoted value, which may span several lines. It returns
// the body with escape sequences still intact and the index of the last line
// consumed.
func readQuoted(lines []string, idx int, first string, quote byte) (string, int, error) {
	var b strings.Builder
	cur := first
	for {
		escaped := false
		for j := range len(cur) {
			c := cur[j]
			switch {
			case escaped:
				// Keep the pair intact; unescapeDouble resolves it later.
				b.WriteByte('\\')
				b.WriteByte(c)
				escaped = false
			case quote == '"' && c == '\\':
				escaped = true
			case c == quote:
				return b.String(), idx, nil
			default:
				b.WriteByte(c)
			}
		}
		if escaped {
			b.WriteByte('\\')
		}
		idx++
		if idx >= len(lines) {
			return "", idx, fmt.Errorf("unterminated %c-quoted value", quote)
		}
		b.WriteByte('\n')
		cur = lines[idx]
	}
}

func unescapeDouble(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\', '"', '\'', '$':
			b.WriteByte(s[i])
		default:
			// Unknown escape: leave it as written.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// expandVars resolves ${VAR}, ${VAR:-default} and $VAR against vals. A
// reference with no match and no default resolves to the empty string. A "$"
// preceded by a backslash is left alone for unescapeDouble to handle.
func expandVars(s string, vals map[string]string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || escapedAt(s, i) || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		name, def, hasDef, width, ok := readVarRef(s[i+1:])
		if !ok {
			b.WriteByte(s[i])
			continue
		}
		v, found := vals[name]
		if !found || (v == "" && hasDef) {
			v = def
		}
		b.WriteString(v)
		i += width
	}
	return b.String()
}

// escapedAt reports whether the byte at i is escaped, which requires an odd
// number of backslashes before it. An even number is a run of escaped
// backslashes, so "C:\\${VAR}" expands while "C:\${VAR}" does not.
func escapedAt(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// readVarRef parses a variable reference immediately after a "$". It returns
// the name, any ":-" default, and how many bytes of s the reference occupied.
func readVarRef(s string) (name, def string, hasDef bool, width int, ok bool) {
	if strings.HasPrefix(s, "{") {
		end := strings.IndexByte(s, '}')
		if end < 0 {
			return "", "", false, 0, false
		}
		body := s[1:end]
		if n, d, found := strings.Cut(body, ":-"); found {
			name, def, hasDef = n, d, true
		} else {
			name = body
		}
		if checkKey(name) != nil {
			return "", "", false, 0, false
		}
		return name, def, hasDef, end + 1, true
	}
	end := 0
	for end < len(s) && isKeyByte(s[end], end == 0) {
		end++
	}
	if end == 0 {
		return "", "", false, 0, false
	}
	return s[:end], "", false, end, true
}

// stripComment removes a trailing comment from an unquoted value. A "#" only
// starts a comment when whitespace precedes it, so that a URL fragment or a
// hex colour survives intact. Callers pass the value with its leading
// whitespace still attached, so that "KEY= # note" is an empty value with a
// comment while "KEY=#0af" is a colour.
func stripComment(s string) string {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

func checkKey(k string) error {
	if k == "" {
		return fmt.Errorf("empty key")
	}
	for i := range len(k) {
		if !isKeyByte(k[i], i == 0) {
			return fmt.Errorf("invalid key %q", k)
		}
	}
	return nil
}

func isKeyByte(c byte, first bool) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		return true
	case !first && c >= '0' && c <= '9':
		return true
	}
	return false
}
