package envx

import (
	"encoding"
	"fmt"
	"reflect"
	"slices"
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
			shown, err = Redacted, redactErr(err, raw)
		}
		d.fail(path, key, shown, origin, err)
	}
}

// redactErr scrubs raw from an error message. Converters build their own
// messages and quote the offending value, and they have no idea the field is a
// secret — so scrub here rather than trust every present and future producer.
func redactErr(err error, raw string) error {
	msg := err.Error()
	if raw == "" || !strings.Contains(msg, raw) {
		return err
	}
	return &redactedError{msg: strings.ReplaceAll(msg, raw, Redacted), cause: err}
}

// redactedError prints a scrubbed message while keeping the original cause
// reachable, so errors.Is and errors.As still match.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.cause }

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

// isSecretType reports whether a field holds secrets, looking through
// pointers, slices and arrays: a []Secret would otherwise have its whole raw
// list printed on failure.
func isSecretType(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	return t.Implements(secretMarkerType)
}
