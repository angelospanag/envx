package envx

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel causes, for use with errors.Is.
var (
	// ErrMissing is the cause when a required key was not supplied by any source.
	ErrMissing = errors.New("required but not set")
	// ErrEmpty is the cause when a notEmpty key was supplied as the empty string.
	ErrEmpty = errors.New("must not be empty")
	// ErrUnsupportedType is the cause when a field's type cannot be decoded.
	ErrUnsupportedType = errors.New("unsupported field type")
	// ErrInvalidTag is the cause when an `env` tag cannot be parsed. Unlike the
	// others it reports a mistake in the code, not in the environment.
	ErrInvalidTag = errors.New("invalid env tag")
)

// FieldError describes one field that could not be loaded.
type FieldError struct {
	// Field is the Go path to the field, such as "APIConfig.DB.Port".
	Field string
	// Key is the environment variable name, such as "DB_PORT".
	Key string
	// Value is the raw value that failed, redacted for a Secret field.
	Value string
	// Origin names the layer the value came from.
	Origin Origin
	// Err is the underlying cause.
	Err error
}

func (fe *FieldError) Error() string {
	var b strings.Builder
	// Environment problems lead with the variable name; struct-definition
	// problems have no variable, so they lead with the field path.
	if fe.Key != "" {
		b.WriteString(fe.Key)
	} else {
		b.WriteString(fe.Field)
	}
	b.WriteString(": ")
	if fe.Err != nil { // exported, so it can arrive without a cause
		b.WriteString(fe.Err.Error())
	} else {
		b.WriteString("invalid")
	}
	if fe.Origin != "" {
		fmt.Fprintf(&b, " (from %s)", fe.Origin)
	}
	return b.String()
}

func (fe *FieldError) Unwrap() error { return fe.Err }

// Error aggregates every problem found in a single Load. One failure at a
// time turns configuring a service into a guessing game, so a field that fails
// never stops the rest from being reported.
type Error struct {
	// Type is the name of the config struct that was being loaded.
	Type string
	// Fields holds the per-field failures, in struct declaration order.
	Fields []FieldError
	// Err holds a failure from the struct's own Validate method.
	Err error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("envx: ")
	switch n := len(e.Fields); {
	case n == 0 && e.Err != nil:
		fmt.Fprintf(&b, "%s failed validation: %s", e.Type, e.Err)
		return b.String()
	case n == 1:
		fmt.Fprintf(&b, "1 problem loading %s:\n", e.Type)
	default:
		fmt.Fprintf(&b, "%d problems loading %s:\n", n, e.Type)
	}
	for i := range e.Fields {
		b.WriteString("  ")
		b.WriteString(e.Fields[i].Error())
		b.WriteByte('\n')
	}
	if e.Err != nil {
		fmt.Fprintf(&b, "  %s\n", e.Err)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Unwrap exposes every cause, so errors.Is and errors.As match any of them.
func (e *Error) Unwrap() []error {
	out := make([]error, 0, len(e.Fields)+1)
	for i := range e.Fields {
		out = append(out, &e.Fields[i])
	}
	if e.Err != nil {
		out = append(out, e.Err)
	}
	return out
}
