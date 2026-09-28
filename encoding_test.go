package envx

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// Windows editors and PowerShell's Out-File prepend a BOM. It is invisible, so
// failing on it produces a baffling error about a key that looks correct.
func TestUTF8BOMIsStripped(t *testing.T) {
	got, err := parseDotEnv("\ufeffPORT=8080\nHOST=x")
	if err != nil {
		t.Fatalf("a BOM must not fail the file: %v", err)
	}
	want := map[string]string{"PORT": "8080", "HOST": "x"}
	if !maps.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestUTF8BOMThroughLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("\ufeffPORT=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	type cfg struct {
		Port int `env:"PORT,default=1"`
	}
	l, err := New(DotEnv(path))
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Load[cfg]()
	if err != nil || got.Port != 8080 {
		t.Errorf("Port = %d, err = %v", got.Port, err)
	}
}

func TestCRLFInsideMultilineValue(t *testing.T) {
	got, err := parseDotEnv("A=\"one\r\ntwo\"\r\nB=2\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "one\ntwo" || got["B"] != "2" {
		t.Errorf("got %#v", got)
	}
}

func TestDegenerateFilesParseEmpty(t *testing.T) {
	for _, in := range []string{"", "\n\n\n", "# just a comment\n", "   \t  \n", "\ufeff"} {
		got, err := parseDotEnv(in)
		if err != nil {
			t.Errorf("parseDotEnv(%q): %v", in, err)
		}
		if len(got) != 0 {
			t.Errorf("parseDotEnv(%q) = %#v, want no keys", in, got)
		}
	}
}

// os.Environ entries split on the first '=' only.
func TestOSEnvSplitsOnFirstEquals(t *testing.T) {
	t.Setenv("ENVX_TEST_EQ", "a=b=c")
	t.Setenv("ENVX_TEST_BLANK", "")

	vals, err := OSEnv().Values()
	if err != nil {
		t.Fatal(err)
	}
	if vals["ENVX_TEST_EQ"] != "a=b=c" {
		t.Errorf("value with '=' = %q", vals["ENVX_TEST_EQ"])
	}
	if v, ok := vals["ENVX_TEST_BLANK"]; !ok || v != "" {
		t.Errorf("an empty value must still be present, got %q ok=%v", v, ok)
	}
}
