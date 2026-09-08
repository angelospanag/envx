package envx

import (
	"errors"
	"strings"
	"testing"
)

type recurNode struct {
	Name string     `env:"NAME"`
	Next *recurNode `envPrefix:"NEXT_"`
}

type selfNoPrefix struct {
	Name string `env:"NAME"`
	Next *selfNoPrefix
}

type mutA struct {
	B *mutB `envPrefix:"B_"`
}

type mutB struct {
	A *mutA `envPrefix:"A_"`
}

// Walking a self-referential type used to recurse until the process died.
func TestRecursiveTypesTerminate(t *testing.T) {
	l, _ := New(Values(map[string]string{"NAME": "root"}))

	// A prefixed group nobody configured simply stops — no error, since
	// nothing asked for it.
	n, err := l.Load[recurNode]()
	if err != nil {
		t.Errorf("unconfigured recursive group should not error, got %v", err)
	}
	if n.Name != "root" || n.Next != nil {
		t.Errorf("got %+v", n)
	}

	if _, err := l.Load[mutA](); err != nil {
		t.Errorf("mutually recursive unconfigured groups should not error, got %v", err)
	}

	// Without a prefix the group would be walked forever, so it is reported.
	_, err = l.Load[selfNoPrefix]()
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("want ErrUnsupportedType for a recursive type, got %v", err)
	}
	if !strings.Contains(err.Error(), "selfNoPrefix.Next: ") {
		t.Errorf("a struct-definition error should lead with the field path, got:\n%v", err)
	}
}

// A pointer group is optional; a value group is always populated.
func TestOptionalPointerGroup(t *testing.T) {
	type replica struct {
		Host string `env:"HOST,required"`
		Port int    `env:"PORT,default=5432"`
	}
	type cfg struct {
		Main    string   `env:"MAIN"`
		Replica *replica `envPrefix:"REPLICA_"`
	}

	// Nothing configured the group, so a required field inside it is not a
	// failure — the whole section is simply absent.
	l, _ := New(Values(map[string]string{"MAIN": "x"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatalf("an unconfigured optional group should not error, got %v", err)
	}
	if got.Replica != nil {
		t.Errorf("Replica = %+v, want nil", got.Replica)
	}

	// One key is enough to bring the group into existence, defaults and all.
	l2, _ := New(Values(map[string]string{"MAIN": "x", "REPLICA_HOST": "r"}))
	got2, err := l2.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got2.Replica == nil || got2.Replica.Host != "r" || got2.Replica.Port != 5432 {
		t.Errorf("Replica = %+v", got2.Replica)
	}

	// Configured but missing a required field is still a failure.
	l3, _ := New(Values(map[string]string{"MAIN": "x", "REPLICA_PORT": "1"}))
	if _, err := l3.Load[cfg](); !errors.Is(err, ErrMissing) {
		t.Errorf("want ErrMissing, got %v", err)
	}
}

// An env name on a nested struct has no meaning, and ignoring it would leave
// every field in the group at its zero value with no explanation.
func TestEnvTagOnNestedStructIsReported(t *testing.T) {
	type inner struct {
		Host string `env:"HOST"`
	}
	type cfg struct {
		DB inner `env:"DATABASE"`
	}
	l, _ := New(Values(map[string]string{"HOST": "h"}))
	_, err := l.Load[cfg]()

	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("want ErrInvalidTag, got %v", err)
	}
	if !strings.Contains(err.Error(), `envPrefix:"DATABASE_"`) {
		t.Errorf("the message should suggest envPrefix, got:\n%v", err)
	}
}

func TestUnbalancedQuoteInTag(t *testing.T) {
	if _, err := parseFieldTag("NAME,default='unclosed"); !errors.Is(err, ErrInvalidTag) {
		t.Errorf("want ErrInvalidTag, got %v", err)
	}
}

// FieldError is exported, so it can be built without a cause.
func TestFieldErrorWithNilCause(t *testing.T) {
	if got := (&FieldError{Key: "K"}).Error(); got != "K: invalid" {
		t.Errorf("got %q", got)
	}
	if got := (&FieldError{Field: "Cfg.F"}).Error(); got != "Cfg.F: invalid" {
		t.Errorf("got %q", got)
	}
}

func TestSliceOfSecrets(t *testing.T) {
	type cfg struct {
		Tokens []Secret[string] `env:"TOKENS"`
	}
	l, _ := New(Values(map[string]string{"TOKENS": "a,b"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tokens) != 2 || got.Tokens[0].Get() != "a" || got.Tokens[1].Get() != "b" {
		t.Fatalf("Tokens = %v", got.Tokens)
	}
	if s := got.Tokens[0].String(); s != Redacted {
		t.Errorf("slice elements must still redact, got %q", s)
	}
}

func TestEmptySourcesAreUsable(t *testing.T) {
	for name, l := range map[string]*Loader{
		"no sources": mustNew(t),
		"nil map":    mustNew(t, Values(nil)),
	} {
		if len(l.Keys()) != 0 {
			t.Errorf("%s: want no keys, got %v", name, l.Keys())
		}
		type cfg struct {
			Port int `env:"PORT,default=8080"`
		}
		got, err := l.Load[cfg]()
		if err != nil || got.Port != 8080 {
			t.Errorf("%s: got %+v err=%v", name, got, err)
		}
	}
}

func mustNew(t *testing.T, sources ...Source) *Loader {
	t.Helper()
	l, err := New(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
