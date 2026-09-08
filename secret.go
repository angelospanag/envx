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

// SecretValue constrains the payload of a [Secret]. Narrow on purpose:
// parsing text into an arbitrary T would mean reflection inside the wrapper,
// and a constraint can be widened later but never narrowed.
type SecretValue interface {
	~string | ~[]byte
}

// Secret wraps a value that must never reach a log line or a serialised
// payload by accident. It redacts itself in String, GoString, Format,
// MarshalText, MarshalJSON and slog output; [Secret.Get] is the one deliberate
// way back to the plaintext. The zero value is a usable empty secret.
type Secret[T SecretValue] struct{ v T }

// NewSecret wraps v.
func NewSecret[T SecretValue](v T) Secret[T] { return Secret[T]{v: v} }

// Get returns the plaintext.
func (s Secret[T]) Get() T { return s.v }

// IsZero reports whether the secret holds an empty value.
func (s Secret[T]) IsZero() bool { return len(s.v) == 0 }

func (s Secret[T]) String() string   { return Redacted }
func (s Secret[T]) GoString() string { return Redacted }

// Format redacts under every verb, including %q and the %d a ~[]byte payload
// would otherwise print. Without it, %+v of the enclosing struct leaks.
func (s Secret[T]) Format(f fmt.State, _ rune) {
	// fmt.Formatter cannot report a write failure, and fmt records it on the
	// State anyway.
	_, _ = io.WriteString(f, Redacted)
}

func (s Secret[T]) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalText redacts; round-tripping a secret is intentionally lossy.
func (s Secret[T]) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

func (s Secret[T]) MarshalJSON() ([]byte, error) { return json.Marshal(Redacted) }

// UnmarshalText is how the decoder populates secret fields.
func (s *Secret[T]) UnmarshalText(b []byte) error {
	s.v = T(bytes.Clone(b))
	return nil
}

// isSecret marks the type for the decoder, which redacts raw values in errors.
// Non-generic so that reflect can see it.
func (Secret[T]) isSecret() {}

// secretMarker is satisfied only by Secret, since isSecret is unexported.
type secretMarker interface{ isSecret() }
