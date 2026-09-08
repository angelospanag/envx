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
	// ErrUnsupportedType is the cause when a struct field's type cannot be
	// decoded from a string.
	ErrUnsupportedType = errors.New("unsupported field type")
	// ErrInvalidTag is the cause when an `env` struct tag cannot be parsed.
	// Unlike the others this reports a mistake in the code, not in the
	// environment.
	ErrInvalidTag = errors.New("invalid env tag")
)

// FieldError describes one field that could not be loaded.
type FieldError struct {
	// Field is the Go path to the field, such as "APIConfig.DB.Port".
	Field string
	// Key is the environment variable name, such as "DB_PORT". This is the
	// part users can act on, so it leads the message.
	Key string
	// Value is the raw value that failed, redacted when the field is a Secret.
	// Empty when nothing was supplied.
	Value string
	// Origin names the layer the value came from. Empty when nothing was
	// supplied.
	Origin Origin
	// Err is the underlying cause.
	Err error
}

func (fe *FieldError) Error() string {
	var b strings.Builder
	// Environment problems lead with the variable name, which is what the
	// reader has to go and change. Problems in the struct definition itself
	// have no variable to name, so they lead with the Go field path.
	if fe.Key != "" {
		b.WriteString(fe.Key)
	} else {
		b.WriteString(fe.Field)
	}
	b.WriteString(": ")
	// FieldError is exported, so it can reach here without a cause.
	if fe.Err != nil {
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

// Error aggregates every problem found in a single Load.
//
// Reporting one failure at a time turns configuring a service into a guessing
// game, so a Load reports every field it can. Parsing and validation are
// necessarily sequential for any single field — a PORT that is not a number
// cannot also be range-checked — but a field that fails to parse never stops
// the others from being reported.
type Error struct {
	// Type is the name of the config struct that was being loaded.
	Type string
	// Fields holds the per-field failures, in struct declaration order.
	Fields []FieldError
	// Err holds a failure from the struct's own Validate method, which runs
	// only once every field has been populated.
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

// Unwrap exposes every cause, so errors.Is and errors.As match against any of
// them.
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
