package envx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const plaintext = "hunter2"

func TestSecretRedactsEveryFormattingVerb(t *testing.T) {
	s := NewSecret(plaintext)
	for _, verb := range []string{"%v", "%+v", "%s", "%q", "%#v", "%d", "%x"} {
		got := fmt.Sprintf(verb, s)
		if strings.Contains(got, plaintext) {
			t.Errorf("fmt.Sprintf(%q, secret) = %s, leaked the value", verb, got)
		}
		if got != Redacted {
			t.Errorf("fmt.Sprintf(%q, secret) = %s, want %s", verb, got, Redacted)
		}
	}
}

// The failure this type exists to prevent: someone %+v's the whole config and
// the password lands in a log line.
func TestSecretRedactsInsideEnclosingStruct(t *testing.T) {
	cfg := struct {
		Host     string
		Password Secret[string]
		Token    Secret[[]byte]
	}{
		Host:     "db.internal",
		Password: NewSecret(plaintext),
		Token:    NewSecret([]byte("tok-abc")),
	}

	for _, verb := range []string{"%v", "%+v", "%#v"} {
		got := fmt.Sprintf(verb, cfg)
		if strings.Contains(got, plaintext) || strings.Contains(got, "tok-abc") {
			t.Errorf("fmt.Sprintf(%q, cfg) = %s, leaked a secret", verb, got)
		}
		if !strings.Contains(got, "db.internal") {
			t.Errorf("fmt.Sprintf(%q, cfg) = %s, should still show non-secret fields", verb, got)
		}
	}
}

func TestSecretRedactsInJSON(t *testing.T) {
	cfg := struct {
		Host     string         `json:"host"`
		Password Secret[string] `json:"password"`
	}{Host: "db.internal", Password: NewSecret(plaintext)}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), plaintext) {
		t.Errorf("json.Marshal leaked the value: %s", b)
	}
	want := `{"host":"db.internal","password":"[redacted]"}`
	if string(b) != want {
		t.Errorf("json.Marshal = %s, want %s", b, want)
	}
}

func TestSecretRedactsInSlog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	s := NewSecret(plaintext)

	log.Info("direct", "password", s)
	log.Info("nested", "config", struct{ Password Secret[string] }{s})

	if strings.Contains(buf.String(), plaintext) {
		t.Errorf("slog leaked the value: %s", buf.String())
	}
	if !strings.Contains(buf.String(), Redacted) {
		t.Errorf("slog output should contain %s: %s", Redacted, buf.String())
	}
}

func TestSecretGetReturnsPlaintext(t *testing.T) {
	if got := NewSecret(plaintext).Get(); got != plaintext {
		t.Errorf("Get() = %q, want %q", got, plaintext)
	}
	if got := NewSecret([]byte("bytes")).Get(); string(got) != "bytes" {
		t.Errorf("Get() = %q", got)
	}
}

func TestSecretZeroValueIsUsable(t *testing.T) {
	var s Secret[string]
	if !s.IsZero() {
		t.Error("zero Secret should report IsZero")
	}
	if s.Get() != "" {
		t.Errorf("Get() = %q, want empty", s.Get())
	}
	if fmt.Sprint(s) != Redacted {
		t.Errorf("zero Secret should still redact, got %s", fmt.Sprint(s))
	}
}

func TestSecretUnmarshalTextCopiesInput(t *testing.T) {
	buf := []byte("original")
	var s Secret[[]byte]
	if err := s.UnmarshalText(buf); err != nil {
		t.Fatal(err)
	}
	copy(buf, "OVERWRIT")
	if string(s.Get()) != "original" {
		t.Errorf("Get() = %q; UnmarshalText must not alias its argument", s.Get())
	}
}

func TestSecretDecodesFromSources(t *testing.T) {
	type cfg struct {
		Password Secret[string] `env:"DB_PASSWORD"`
		Token    Secret[[]byte] `env:"TOKEN"`
	}
	l, _ := New(Values(map[string]string{"DB_PASSWORD": plaintext, "TOKEN": "tok-abc"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Password.Get() != plaintext {
		t.Errorf("Password = %q", got.Password.Get())
	}
	if string(got.Token.Get()) != "tok-abc" {
		t.Errorf("Token = %q", got.Token.Get())
	}
}

// A Secret field must be treated as a leaf, not walked into as a nested
// struct, or its unexported payload would be invisible to the decoder.
func TestSecretIsALeafNotANestedStruct(t *testing.T) {
	st := reflect.TypeFor[Secret[string]]()
	if isNested(st) {
		t.Error("Secret should decode as a leaf")
	}
	if !isSecretType(st) {
		t.Error("isSecretType should recognise Secret")
	}
	if !isSecretType(reflect.TypeFor[*Secret[string]]()) {
		t.Error("isSecretType should recognise *Secret")
	}
	if isSecretType(reflect.TypeFor[string]()) {
		t.Error("isSecretType should not match a plain string")
	}
}

// Secrets are file-mounted as often as they are set in the environment.
func TestSecretFromSecretsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "DB_PASSWORD"),
		[]byte(plaintext+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	type cfg struct {
		Password Secret[string] `env:"DB_PASSWORD"`
	}
	l, err := New(SecretsDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Password.Get() != plaintext {
		t.Errorf(
			"Password = %q, want %q (trailing newline stripped)",
			got.Password.Get(),
			plaintext,
		)
	}
}
