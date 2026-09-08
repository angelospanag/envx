package envx

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
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

// A UTF-16 file cannot be parsed as UTF-8, so say that rather than complain
// about an unreadable key name.
func TestUTF16IsReportedClearly(t *testing.T) {
	for _, bom := range []string{"\xff\xfe", "\xfe\xff"} {
		_, err := parseDotEnv(bom + "PORT=8080")
		if err == nil {
			t.Fatalf("BOM %q should be rejected", bom)
		}
		if !strings.Contains(err.Error(), "UTF-16") {
			t.Errorf("error should name the encoding, got %v", err)
		}
	}
}

// Dropping what follows a closing quote would silently truncate the value.
func TestContentAfterClosingQuote(t *testing.T) {
	rejected := []string{
		`A="x"y`,
		`A="x" trailing`,
		`A='x'y`,
		`A="x""y"`,
		"A=\"one\ntwo\"tail",
	}
	for _, in := range rejected {
		if _, err := parseDotEnv(in); err == nil {
			t.Errorf("parseDotEnv(%q) was accepted; the tail would be dropped", in)
		} else if !strings.Contains(err.Error(), "after the closing quote") {
			t.Errorf("parseDotEnv(%q): %v", in, err)
		}
	}

	accepted := map[string]string{
		`A="x"`:           "x",
		`A="x" `:          "x",
		`A="x" # comment`: "x",
		`A="x"# comment`:  "x",
		`A='x'	# tabbed`:  "x",
	}
	for in, want := range accepted {
		got, err := parseDotEnv(in)
		if err != nil {
			t.Errorf("parseDotEnv(%q): %v", in, err)
			continue
		}
		if got["A"] != want {
			t.Errorf("parseDotEnv(%q) = %q, want %q", in, got["A"], want)
		}
	}
}

// The reported line must account for lines consumed by a multi-line value.
func TestLineNumbersSurviveMultilineValues(t *testing.T) {
	_, err := parseDotEnv("A=1\nB=\"one\ntwo\nthree\"\nthis is not valid\n")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "line 5") {
		t.Errorf("want line 5, got %v", err)
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
