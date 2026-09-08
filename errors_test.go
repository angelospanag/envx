package envx

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A blind ReplaceAll would rewrite the message text itself whenever the secret
// is short or happens to be a word of the error. Replacing the %q form keeps
// the useful message; anything still colliding degrades to a detail-free one
// rather than to nonsense.
func TestRedactErrDoesNotMangleTheMessage(t *testing.T) {
	tests := map[string]string{
		"hunter2":      `must be an integer, got "[redacted]"`,
		"s3cr3t-value": `must be an integer, got "[redacted]"`,
		"a":            "invalid value [redacted]",
		"e":            "invalid value [redacted]",
		"integer":      "invalid value [redacted]",
		"must":         "invalid value [redacted]",
	}
	for raw, want := range tests {
		cause := fmt.Errorf("must be an integer, got %q", raw)
		got := redactErr(cause, raw).Error()
		if got != want {
			t.Errorf("raw=%q:\n got %q\nwant %q", raw, got, want)
		}
		// A one- or two-character secret occurs in ordinary English by
		// coincidence, so only a distinctive value proves anything here.
		if len(raw) > 3 && strings.Contains(got, raw) {
			t.Errorf("raw=%q leaked: %q", raw, got)
		}
	}
}

func TestRedactErrKeepsTheCauseMatchable(t *testing.T) {
	for _, raw := range []string{"hunter2", "a"} {
		cause := fmt.Errorf("%w: got %q", ErrUnsupportedType, raw)
		if got := redactErr(cause, raw); !errors.Is(got, ErrUnsupportedType) {
			t.Errorf("raw=%q: errors.Is should match through the redaction", raw)
		}
	}
}

func TestRedactErrPassesThroughUnrelatedErrors(t *testing.T) {
	cause := errors.New("required but not set")
	if got := redactErr(cause, "hunter2"); got != cause {
		t.Error("an error not mentioning the value should be returned unchanged")
	}
	if got := redactErr(cause, ""); got != cause {
		t.Error("an empty raw value should be returned unchanged")
	}
}

func TestErrorRendersEveryShape(t *testing.T) {
	tests := map[string]struct {
		err  *Error
		want string
	}{
		"no causes at all": {
			&Error{Type: "Config"},
			"envx: Config failed to load",
		},
		"validation only": {
			&Error{Type: "Config", Err: errors.New("cross-field rule failed")},
			"envx: Config failed validation: cross-field rule failed",
		},
		"one field": {
			&Error{Type: "Config", Fields: []FieldError{{Key: "PORT", Err: errors.New("bad")}}},
			"envx: 1 problem loading Config:\n  PORT: bad",
		},
	}
	for name, tc := range tests {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

func TestErrorUnwrapCoversFieldsAndValidation(t *testing.T) {
	e := &Error{
		Type:   "Config",
		Fields: []FieldError{{Key: "PORT", Err: ErrMissing}},
		Err:    errors.New("cross-field rule failed"),
	}
	if n := len(e.Unwrap()); n != 2 {
		t.Errorf("Unwrap() returned %d causes, want 2", n)
	}
	if !errors.Is(e, ErrMissing) {
		t.Error("errors.Is should reach a field cause")
	}
}

// A value containing format verbs must not be interpreted as a format string.
func TestFormatVerbsInValueAreLiteral(t *testing.T) {
	type cfg struct {
		N int `env:"N"`
	}
	l, _ := New(Values(map[string]string{"N": "100%%d %s %!v(MISSING)"}))
	_, err := l.Load[cfg]()
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), `100%%d %s %!v(MISSING)`) {
		t.Errorf("value should appear verbatim: %v", err)
	}
}

// Arrays are not a supported field type; the error should say so plainly.
func TestArrayFieldIsReported(t *testing.T) {
	type cfg struct {
		A [3]string `env:"A"`
	}
	l, _ := New(Values(map[string]string{"A": "x,y,z"}))
	_, err := l.Load[cfg]()
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("want ErrUnsupportedType, got %v", err)
	}
}

// The two precedence rules act on different axes and must both hold.
func TestPrecedenceAxes(t *testing.T) {
	m, err := parseDotEnv("A=1\nA=2")
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != "2" {
		t.Errorf("within a file the last assignment wins, got %q", m["A"])
	}

	l, _ := New(
		NamedValues("first", map[string]string{"A": "first"}),
		NamedValues("second", map[string]string{"A": "second"}),
	)
	if v, _, _ := l.lookup("A"); v != "first" {
		t.Errorf("across layers the earlier source wins, got %q", v)
	}
}
