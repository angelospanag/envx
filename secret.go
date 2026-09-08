package envx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// Redacted is the placeholder substituted for secret values in every
// formatting, logging and marshalling path.
const Redacted = "[redacted]"

// SecretValue constrains the payload of a Secret.
//
// The constraint is deliberately narrow. Parsing text into an arbitrary T
// would mean reflection inside the wrapper; string-like types cover the
// passwords, tokens and keys that actually arrive through the environment.
// Widening a constraint later is backwards compatible, narrowing one is not.
type SecretValue interface {
	~string | ~[]byte
}

// Secret wraps a value that must never reach a log line or a serialised
// payload by accident. It redacts itself in String, GoString, Format,
// MarshalText, MarshalJSON and slog output; Get is the single deliberate way
// back to the plaintext.
//
// The zero value is a usable empty secret.
type Secret[T SecretValue] struct{ v T }

// NewSecret wraps v.
func NewSecret[T SecretValue](v T) Secret[T] { return Secret[T]{v: v} }

// Get returns the underlying value.
func (s Secret[T]) Get() T { return s.v }

// IsZero reports whether the secret holds an empty value.
func (s Secret[T]) IsZero() bool { return len(s.v) == 0 }

func (s Secret[T]) String() string   { return Redacted }
func (s Secret[T]) GoString() string { return Redacted }

// Format satisfies fmt.Formatter so that every verb redacts, including %q and
// the %d you would otherwise get from a ~[]byte payload. Without it, a
// `%+v` of the enclosing config struct would print the secret.
func (s Secret[T]) Format(f fmt.State, _ rune) {
	// fmt.Formatter cannot report a write failure, and fmt records it on the
	// State anyway.
	_, _ = io.WriteString(f, Redacted)
}

// LogValue satisfies slog.LogValuer.
func (s Secret[T]) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalText satisfies encoding.TextMarshaler. Round-tripping a secret is
// intentionally lossy.
func (s Secret[T]) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// MarshalJSON satisfies json.Marshaler.
func (s Secret[T]) MarshalJSON() ([]byte, error) { return json.Marshal(Redacted) }

// UnmarshalText satisfies encoding.TextUnmarshaler, which is how the decoder
// populates secret fields.
func (s *Secret[T]) UnmarshalText(b []byte) error {
	s.v = T(bytes.Clone(b))
	return nil
}

// isSecret marks the type for the decoder, which redacts raw values in error
// output. It is a plain method rather than a generic one so that reflect can
// still see it.
func (Secret[T]) isSecret() {}

// secretMarker is satisfied only by Secret, since isSecret is unexported.
type secretMarker interface{ isSecret() }
