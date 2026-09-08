package envx

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Converters quote the offending value in their own messages and have no idea
// the field is a secret, so the message — not just FieldError.Value, which is
// never rendered — has to be scrubbed.
func TestRedactErrScrubsTheMessage(t *testing.T) {
	raw := "hunter2"
	cause := fmt.Errorf("%w: must be at least 32 characters, got %q", ErrUnsupportedType, raw)

	got := redactErr(cause, raw)
	if strings.Contains(got.Error(), raw) {
		t.Errorf("message still leaks the secret: %s", got)
	}
	if !strings.Contains(got.Error(), Redacted) {
		t.Errorf("message should show %s: %s", Redacted, got)
	}
	// The cause stays reachable, so errors.Is keeps working.
	if !errors.Is(got, ErrUnsupportedType) {
		t.Error("errors.Is should still match through a redacted error")
	}
}

func TestRedactErrLeavesUnrelatedErrorsAlone(t *testing.T) {
	cause := errors.New("must be an integer")
	if got := redactErr(cause, "hunter2"); got != cause {
		t.Errorf("an error not containing the value should pass through unchanged")
	}
	if got := redactErr(cause, ""); got != cause {
		t.Errorf("an empty raw value should pass through unchanged")
	}
}

// The redaction is only as good as the detection: a slice of secrets counts.
func TestIsSecretTypeLooksThroughContainers(t *testing.T) {
	secret := []reflect.Type{
		reflect.TypeFor[Secret[string]](),
		reflect.TypeFor[*Secret[string]](),
		reflect.TypeFor[[]Secret[string]](),
		reflect.TypeFor[[2]Secret[string]](),
		reflect.TypeFor[[]*Secret[[]byte]](),
	}
	for _, tp := range secret {
		if !isSecretType(tp) {
			t.Errorf("isSecretType(%s) = false, want true", tp)
		}
	}
	for _, tp := range []reflect.Type{
		reflect.TypeFor[string](),
		reflect.TypeFor[[]byte](),
		reflect.TypeFor[[]string](),
		reflect.TypeFor[int](),
	} {
		if isSecretType(tp) {
			t.Errorf("isSecretType(%s) = true, want false", tp)
		}
	}
}

// The failure paths a secret field can reach today must not carry its value.
func TestSecretFailuresCarryNoValue(t *testing.T) {
	type cfg struct {
		A Secret[string] `env:"A,notEmpty"`
		B Secret[string] `env:"B,required"`
	}
	l, _ := New(Values(map[string]string{"A": "hunter2-but-empty-check"}))
	_, err := l.Load[cfg]()

	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *Error, got %v", err)
	}
	for _, fe := range e.Fields {
		if fe.Value != "" && fe.Value != Redacted {
			t.Errorf("FieldError.Value = %q; a secret must never expose its raw value", fe.Value)
		}
	}
}

// WithSources must not keep the caller's backing array: passing srcs... shares
// it, so a later write would change what gets loaded.
func TestWithSourcesCopiesTheSlice(t *testing.T) {
	srcs := []Source{Values(map[string]string{"PORT": "3"})}
	opt := WithSources(srcs...)
	srcs[0] = Values(map[string]string{"PORT": "4"})

	var o loadOptions
	opt(&o)
	l, err := New(o.sources...)
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := l.lookup("PORT"); v != "3" {
		t.Errorf("PORT = %q, want 3; WithSources aliased the caller's slice", v)
	}
}

func TestDefaultSourcesIsFreshEachCall(t *testing.T) {
	a, b := DefaultSources(), DefaultSources()
	if len(a) == 0 {
		t.Fatal("no default sources")
	}
	if &a[0] == &b[0] {
		t.Error("DefaultSources should return a fresh slice each call")
	}
}

func TestPackageLevelLoadAndMust(t *testing.T) {
	type cfg struct {
		Port int `env:"PORT,default=8080"`
	}
	got, err := Load[cfg](WithSources(Values(map[string]string{"PORT": "1"})))
	if err != nil || got.Port != 1 {
		t.Errorf("Load: %+v err=%v", got, err)
	}
	if m := Must[cfg](WithSources(Values(nil))); m.Port != 8080 {
		t.Errorf("Must: %+v", m)
	}

	defer func() {
		if recover() == nil {
			t.Error("Must should panic on an invalid config")
		}
	}()
	type bad struct {
		Port int `env:"PORT,required"`
	}
	_ = Must[bad](WithSources(Values(nil)))
}
