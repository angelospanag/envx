package envx

import (
	"encoding"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	secretMarkerType    = reflect.TypeFor[secretMarker]()
	durationType        = reflect.TypeFor[time.Duration]()
)

// OriginDefault is the Origin reported for a value that came from a struct
// tag default rather than from any source.
const OriginDefault Origin = "default"

// fieldOpts holds the parsed `env` struct tag.
type fieldOpts struct {
	name       string
	def        string
	hasDefault bool
	required   bool
	notEmpty   bool
	separator  string
}

// parseFieldTag parses the `env` struct tag.
//
// An unrecognised option is an error rather than something to skip. Options are
// comma-separated, so silently ignoring them would turn `default=a,b` into the
// default "a" with the "b" quietly dropped, and would swallow a miscased
// `notempty`. Wrap a value in single quotes to include a comma in it.
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
			// An empty separator would split a value into single characters.
			// It is also what `separator=,` parses to, since the comma is
			// consumed as the option delimiter — so read it as the default.
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

// nextTagOption splits off the next comma-separated option, honouring single
// quotes so that a quoted value may contain commas.
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
		return v, nil
	}
	if len(v) < 2 || !strings.HasSuffix(v, "'") {
		return "", fmt.Errorf("%w: unbalanced quote in %q", ErrInvalidTag, v)
	}
	return v[1 : len(v)-1], nil
}

type decoder struct {
	l    *Loader
	errs []FieldError
	// stack holds the struct types currently being walked, so that a
	// self-referential config type is caught rather than recursed into
	// forever.
	stack []reflect.Type
}

// walk populates rv, appending a FieldError for every field it cannot fill
// rather than stopping at the first.
func (d *decoder) walk(rv reflect.Value, prefix, path string) {
	rt := rv.Type()
	d.stack = append(d.stack, rt)
	defer func() { d.stack = d.stack[:len(d.stack)-1] }()

	for i := range rt.NumField() {
		sf := rt.Field(i)
		if !sf.IsExported() && !promotesSettableFields(sf) {
			continue
		}
		tag, hasTag := sf.Tag.Lookup("env")
		if tag == "-" {
			continue
		}
		fv := rv.Field(i)
		fieldPath := path + "." + sf.Name

		if isNested(sf.Type) {
			nested := fieldPath
			if sf.Anonymous {
				// An embedded struct's fields are promoted, so it contributes
				// no segment of its own to the path.
				nested = path
			}
			groupPrefix := prefix + sf.Tag.Get("envPrefix")

			// A nested struct is addressed by prefixing its fields, so an env
			// name on one has no meaning. Reporting it beats ignoring it: the
			// author meant envPrefix and would otherwise get zero values.
			if n, _, _ := strings.Cut(tag, ","); strings.TrimSpace(n) != "" {
				d.fail(fieldPath, "", "", "", fmt.Errorf(
					"%w: %s is a nested struct, so it takes envPrefix:%q, not env:%q",
					ErrInvalidTag, sf.Name, strings.TrimSpace(n)+"_", strings.TrimSpace(n),
				))
				continue
			}
			d.walkNested(fv, groupPrefix, nested, fieldPath)
			continue
		}

		opts, err := parseFieldTag(tag)
		if !hasTag || opts.name == "" {
			opts.name = screamingSnake(sf.Name)
		}
		if err != nil {
			d.fail(fieldPath, prefix+opts.name, "", "", err)
			continue
		}
		d.leaf(fv, prefix+opts.name, fieldPath, opts, isSecretType(sf.Type))
	}
}

// promotesSettableFields reports whether an unexported field should still be
// walked.
//
// Embedding an unexported struct type is a common way to share config fields,
// and reflect permits setting the exported fields promoted from it even though
// the embedded field itself reports CanSet false. An embedded unexported
// pointer or scalar has no such exception — setting one panics — so only the
// struct case qualifies, and only when we would recurse rather than treat it
// as a leaf.
func promotesSettableFields(sf reflect.StructField) bool {
	return sf.Anonymous && sf.Type.Kind() == reflect.Struct && isNested(sf.Type)
}

func (d *decoder) walkNested(fv reflect.Value, prefix, path, fieldPath string) {
	ptr := fv.Kind() == reflect.Pointer
	if ptr && fv.IsNil() {
		// A pointer group is optional: it is populated only when some source
		// actually configured it. A value struct is always populated, defaults
		// included — that difference is how a config says "this section may be
		// absent". Deciding this before the cycle check means a recursive type
		// nobody configured simply stops, rather than being reported.
		if !fv.CanSet() || !d.l.hasPrefix(prefix) {
			return
		}
	}

	// A type that contains itself cannot be expressed as a flat set of
	// environment variables, and walking it would not terminate.
	if st := derefType(fv.Type()); slices.Contains(d.stack, st) {
		d.fail(fieldPath, "", "", "", fmt.Errorf(
			"%w: %s is recursive", ErrUnsupportedType, st,
		))
		return
	}

	if ptr {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		fv = fv.Elem()
	}
	d.walk(fv, prefix, path)
}

func derefType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

func (d *decoder) leaf(fv reflect.Value, key, path string, opts fieldOpts, secret bool) {
	raw, origin, found := d.l.lookup(key)
	if !found {
		switch {
		case opts.hasDefault:
			raw, origin = opts.def, OriginDefault
		case opts.required:
			d.fail(path, key, "", "", ErrMissing)
			return
		default:
			return // leave the zero value in place
		}
	}
	if raw == "" && opts.notEmpty {
		d.fail(path, key, "", origin, ErrEmpty)
		return
	}
	if err := setValue(fv, raw, opts.separator); err != nil {
		shown := raw
		if secret {
			shown = Redacted
		}
		d.fail(path, key, shown, origin, err)
	}
}

func (d *decoder) fail(path, key, value string, origin Origin, err error) {
	d.errs = append(d.errs, FieldError{
		Field:  path,
		Key:    key,
		Value:  value,
		Origin: origin,
		Err:    err,
	})
}

// isNested reports whether a field should be recursed into rather than parsed
// from a single string. A struct that knows how to parse itself — time.Time,
// netip.Addr, Secret — is a leaf.
func isNested(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	return !reflect.PointerTo(t).Implements(textUnmarshalerType)
}

func isSecretType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Implements(secretMarkerType)
}

// setValue parses raw into fv.
func setValue(fv reflect.Value, raw, sep string) error {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return setValue(fv.Elem(), raw, sep)
	}

	// A type that can parse itself always wins, which is what makes Secret,
	// time.Time and any user type work without a special case here.
	if fv.CanAddr() {
		if u, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
			if err := u.UnmarshalText([]byte(raw)); err != nil {
				return fmt.Errorf("must be a valid %s: %w", fv.Type(), err)
			}
			return nil
		}
	}

	if fv.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("must be a duration such as 30s or 5m, got %q", raw)
		}
		fv.SetInt(int64(d))
		return nil
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
		return nil

	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("must be a boolean such as true or false, got %q", raw)
		}
		fv.SetBool(b)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return numErr(err, raw, "an integer", fv.Type())
		}
		fv.SetInt(n)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return numErr(err, raw, "a non-negative integer", fv.Type())
		}
		fv.SetUint(n)
		return nil

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return numErr(err, raw, "a number", fv.Type())
		}
		fv.SetFloat(f)
		return nil

	case reflect.Slice:
		return setSlice(fv, raw, sep)

	default:
		return fmt.Errorf("%w %s", ErrUnsupportedType, fv.Type())
	}
}

func setSlice(fv reflect.Value, raw, sep string) error {
	// []byte is a payload, not a list.
	if fv.Type().Elem().Kind() == reflect.Uint8 && fv.Type().Elem().PkgPath() == "" {
		fv.SetBytes([]byte(raw))
		return nil
	}
	if raw == "" {
		fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
		return nil
	}
	parts := strings.Split(raw, sep)
	out := reflect.MakeSlice(fv.Type(), len(parts), len(parts))
	for i, p := range parts {
		if err := setValue(out.Index(i), strings.TrimSpace(p), sep); err != nil {
			return fmt.Errorf("item %d: %w", i+1, err)
		}
	}
	fv.Set(out)
	return nil
}

func numErr(err error, raw, want string, t reflect.Type) error {
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return fmt.Errorf("must fit in %s, got %q", t, raw)
	}
	return fmt.Errorf("must be %s, got %q", want, raw)
}

// screamingSnake derives an environment variable name from a Go field name.
// It keeps acronym runs together, so DBPassword becomes DB_PASSWORD rather
// than D_B_PASSWORD, and HTTPPort becomes HTTP_PORT.
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
