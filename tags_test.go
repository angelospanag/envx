package envx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envPrefix means something only on a nested struct. Left on a leaf it would
// silently read the unprefixed variable — the wrong one.
func TestEnvPrefixOnLeafIsReported(t *testing.T) {
	type cfg struct {
		Port int `env:"PORT" envPrefix:"DB_"`
	}
	l, _ := New(Values(map[string]string{"PORT": "1", "DB_PORT": "2"}))
	got, err := l.Load[cfg]()

	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("want ErrInvalidTag, got %v", err)
	}
	if got.Port != 0 {
		t.Errorf("Port = %d, want the zero value", got.Port)
	}
	if !strings.Contains(err.Error(), `env:"DB_PORT"`) {
		t.Errorf("the message should suggest the combined name: %v", err)
	}
}

func TestEnvPrefixStillWorksOnNestedStructs(t *testing.T) {
	type inner struct {
		Port int `env:"PORT"`
	}
	type cfg struct {
		DB inner `envPrefix:"DB_"`
	}
	l, _ := New(Values(map[string]string{"PORT": "1", "DB_PORT": "2"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.DB.Port != 2 {
		t.Errorf("DB.Port = %d, want 2", got.DB.Port)
	}
}

// An unquoted tag value is trimmed; quoting preserves whitespace exactly.
func TestTagValueWhitespace(t *testing.T) {
	type cfg struct {
		Port int    `env:"PORT,default= 8080"`
		Pad  string `env:"PAD,default=' x '"`
	}
	l, _ := New(Values(nil))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want 8080", got.Port)
	}
	if got.Pad != " x " {
		t.Errorf("Pad = %q, want %q", got.Pad, " x ")
	}
}

type failingSource struct{ name string }

func (s failingSource) Name() string { return s.name }
func (s failingSource) Values() (map[string]string, error) {
	return nil, errors.New("upstream unavailable")
}

// A bare error from a custom Source says nothing about which layer failed, so
// New adds the name.
func TestSourceErrorNamesTheLayer(t *testing.T) {
	_, err := New(Values(map[string]string{"A": "1"}), failingSource{name: "vault"})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "vault") {
		t.Errorf("error should name the failing source: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream unavailable") {
		t.Errorf("error should keep the cause: %v", err)
	}
}

func TestMalformedDotEnvNamesFileAndLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GOOD=1\nthis is not valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(DotEnv(path))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{path, "line 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q: %v", want, err)
		}
	}
}

// A Source hands over a map; the Loader must not alias it.
func TestSourceMapIsCopied(t *testing.T) {
	m := map[string]string{"PORT": "8080"}
	l, _ := New(Values(m))
	m["PORT"] = "9999"
	m["NEW"] = "x"

	if v, _, _ := l.lookup("PORT"); v != "8080" {
		t.Errorf("PORT = %q; the snapshot must not alias the caller's map", v)
	}
	if _, _, ok := l.lookup("NEW"); ok {
		t.Error("a key added after New must not appear in the snapshot")
	}
}

// env:"-" skips a nested struct entirely, so a required field inside it is
// never consulted.
func TestSkipTagOnNestedStruct(t *testing.T) {
	type inner struct {
		Host string `env:"HOST,required"`
	}
	type cfg struct {
		Skip inner  `env:"-"`
		Name string `env:"NAME"`
	}
	l, _ := New(Values(map[string]string{"NAME": "n"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatalf("a skipped group must not be validated: %v", err)
	}
	if got.Name != "n" || got.Skip.Host != "" {
		t.Errorf("got %+v", got)
	}
}

func TestDegenerateStructsLoad(t *testing.T) {
	l, _ := New(Values(map[string]string{"X": "1"}))

	type empty struct{}
	if _, err := l.Load[empty](); err != nil {
		t.Errorf("empty struct: %v", err)
	}

	type unexportedOnly struct{ a, b int }
	got, err := l.Load[unexportedOnly]()
	if err != nil {
		t.Errorf("unexported-only struct: %v", err)
	}
	if got.a != 0 || got.b != 0 {
		t.Errorf("unexported fields must be left alone, got %+v", got)
	}
}

// Two fields may claim the same variable; both are populated.
func TestTwoFieldsMayShareAKey(t *testing.T) {
	type cfg struct {
		A int `env:"PORT"`
		B int `env:"PORT"`
	}
	l, _ := New(Values(map[string]string{"PORT": "80"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.A != 80 || got.B != 80 {
		t.Errorf("got %+v", got)
	}
}

func ExampleLoader_Origin() {
	l, _ := New(
		NamedValues("OS environment", map[string]string{"PORT": "9090"}),
		NamedValues(".env", map[string]string{"PORT": "8080", "HOST": "localhost"}),
	)
	port, _ := l.Origin("PORT")
	host, _ := l.Origin("HOST")
	fmt.Println(port)
	fmt.Println(host)
	// Output:
	// OS environment
	// .env
}
