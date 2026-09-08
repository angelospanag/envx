package envx

import (
	"encoding"
	"fmt"
	"math"
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

// OriginDefault marks a value that came from a tag default, not a source.
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

type decoder struct {
	l    *Loader
	errs []FieldError
	// stack holds the struct types currently being walked, catching a
	// self-referential type instead of recursing forever.
	stack []reflect.Type
}

// walk populates rv, appending a FieldError per unfillable field rather than
// stopping at the first.
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
				nested = path // promoted fields add no path segment
			}
			groupPrefix := prefix + sf.Tag.Get("envPrefix")

			// A nested struct is addressed by prefixing its fields, so an env
			// name means nothing here. The author meant envPrefix, and would
			// otherwise silently get zero values.
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

		// envPrefix only means something on a nested struct. Left on a leaf it
		// would silently read the unprefixed variable instead.
		if p, ok := sf.Tag.Lookup("envPrefix"); ok {
			d.fail(fieldPath, "", "", "", fmt.Errorf(
				"%w: envPrefix:%q on a non-struct field; did you mean env:%q?",
				ErrInvalidTag, p, p+opts.name,
			))
			continue
		}
		d.leaf(fv, prefix+opts.name, fieldPath, opts, isSecretType(sf.Type))
	}
}

// promotesSettableFields reports whether an unexported field is still worth
// walking. reflect permits setting fields promoted from an embedded unexported
// struct even though the field itself reports CanSet false; an embedded
// unexported pointer or scalar panics instead, so only the struct qualifies.
func promotesSettableFields(sf reflect.StructField) bool {
	return sf.Anonymous && sf.Type.Kind() == reflect.Struct && isNested(sf.Type)
}

func (d *decoder) walkNested(fv reflect.Value, prefix, path, fieldPath string) {
	ptr := fv.Kind() == reflect.Pointer
	if ptr && fv.IsNil() {
		// A pointer group is optional, populated only when a source supplied a
		// key under its prefix; a value struct is always populated. Deciding
		// this before the cycle check lets an unconfigured recursive type stop
		// quietly. Without a prefix the group shares its parent's namespace, so
		// it is always populated rather than hinging on unrelated keys.
		if !fv.CanSet() || (prefix != "" && !d.l.hasPrefix(prefix)) {
			return
		}
	}

	// A self-containing type cannot be expressed as flat variables, and walking
	// it would not terminate.
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

// isNested reports whether a field is recursed into rather than parsed from a
// string. A struct that parses itself — time.Time, Secret — is a leaf.
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

	// A type that parses itself wins, which is what makes Secret, time.Time and
	// any user type work without a special case.
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
		// NaN and Inf are never deliberate config values and fail silently:
		// every comparison against NaN is false, so a threshold stops working.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("must be a finite number, got %q", raw)
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
	out := reflect.MakeSlice(fv.Type(), 0, len(parts))
	for i, p := range parts {
		// A trailing or doubled separator is the commonest way to mistype a
		// list; an empty item would become a host or token failing much later.
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		elem := reflect.New(fv.Type().Elem()).Elem()
		if err := setValue(elem, p, sep); err != nil {
			return fmt.Errorf("item %d: %w", i+1, err)
		}
		out = reflect.Append(out, elem)
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
