package envx

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestParseDotEnv(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"simple", "A=1\nB=two\n", map[string]string{"A": "1", "B": "two"}},
		{
			"blank and comments",
			"\n# a comment\nA=1\n\n  # indented\nB=2",
			map[string]string{"A": "1", "B": "2"},
		},
		{"export prefix", "export A=1\nexport  B=2", map[string]string{"A": "1", "B": "2"}},
		{"spaces around equals", "A = 1\nB\t=\t2", map[string]string{"A": "1", "B": "2"}},
		{"set but empty", "A=\nB= \n", map[string]string{"A": "", "B": ""}},
		{
			"trailing comment",
			"A=one # trailing\nB=two\t# tabbed",
			map[string]string{"A": "one", "B": "two"},
		},
		{
			"hash without space is literal",
			"URL=http://x/#frag\nC=#0af",
			map[string]string{"URL": "http://x/#frag", "C": "#0af"},
		},
		{"double quotes keep spaces", `A="  padded  "`, map[string]string{"A": "  padded  "}},
		{
			"double quote escapes",
			`A="line\nnext\ttab\"q\\slash"`,
			map[string]string{"A": "line\nnext\ttab\"q\\slash"},
		},
		{
			"single quotes are literal",
			`A='no \n escape $NOPE'`,
			map[string]string{"A": `no \n escape $NOPE`},
		},
		{"hash inside quotes survives", `A="a # b"`, map[string]string{"A": "a # b"}},
		{"unknown escape kept", `A="c:\path"`, map[string]string{"A": `c:\path`}},
		{"crlf", "A=1\r\nB=2\r\n", map[string]string{"A": "1", "B": "2"}},
		{
			"multiline double",
			"KEY=\"line one\nline two\"\nAFTER=yes",
			map[string]string{"KEY": "line one\nline two", "AFTER": "yes"},
		},
		{
			"multiline single",
			"KEY='line one\nline two'\nAFTER=yes",
			map[string]string{"KEY": "line one\nline two", "AFTER": "yes"},
		},
		{
			"expansion braced",
			"BASE=/srv\nP=${BASE}/a",
			map[string]string{"BASE": "/srv", "P": "/srv/a"},
		},
		{
			"expansion bare",
			"BASE=/srv\nP=$BASE/a",
			map[string]string{"BASE": "/srv", "P": "/srv/a"},
		},
		{
			"expansion in double quotes",
			"BASE=/srv\nP=\"${BASE}/a\"",
			map[string]string{"BASE": "/srv", "P": "/srv/a"},
		},
		{"expansion unresolved is empty", "P=x${NOPE}y", map[string]string{"P": "xy"}},
		{"expansion default", "P=${NOPE:-fallback}", map[string]string{"P": "fallback"}},
		{
			"expansion default unused",
			"A=real\nP=${A:-fallback}",
			map[string]string{"A": "real", "P": "real"},
		},
		{"escaped dollar unquoted", `P=\$NOTAVAR`, map[string]string{"P": "$NOTAVAR"}},
		{"escaped dollar quoted", `P="\$NOTAVAR"`, map[string]string{"P": "$NOTAVAR"}},
		{"later key wins within a file", "A=1\nA=2", map[string]string{"A": "2"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDotEnv(tc.in)
			if err != nil {
				t.Fatalf("parseDotEnv: %v", err)
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("parseDotEnv(%q)\n got %#v\nwant %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDotEnvErrors(t *testing.T) {
	tests := []struct{ name, in string }{
		{"no equals", "JUST_A_KEY\n"},
		{"empty key", "=value\n"},
		{"invalid key", "not-a-key=1\n"},
		{"key starting with digit", "1BAD=1\n"},
		{"unterminated double quote", "A=\"never closed\n"},
		{"unterminated single quote", "A='never closed\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseDotEnv(tc.in); err == nil {
				t.Errorf("parseDotEnv(%q) = nil error, want an error", tc.in)
			}
		})
	}
}

// The whole layering design rests on .env files staying out of the process
// environment, so guard it directly.
func TestDotEnvDoesNotTouchProcessEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("ENVX_MUST_NOT_LEAK=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	vals, err := DotEnv(path).Values()
	if err != nil {
		t.Fatal(err)
	}
	if vals["ENVX_MUST_NOT_LEAK"] != "1" {
		t.Fatalf("parsed value missing: %#v", vals)
	}
	if v, ok := os.LookupEnv("ENVX_MUST_NOT_LEAK"); ok {
		t.Errorf("dotenv leaked into os.Environ as %q", v)
	}
}

func TestDotEnvMissingFileIsNotAnError(t *testing.T) {
	vals, err := DotEnv(filepath.Join(t.TempDir(), "absent")).Values()
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(vals) != 0 {
		t.Errorf("want no values, got %#v", vals)
	}
}

func TestSecretsDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("db_password", "hunter2\n")
	write("api_token", "tok")
	write(".hidden", "skipped")
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}

	vals, err := SecretsDir(dir).Values()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"db_password": "hunter2", "api_token": "tok"}
	if !maps.Equal(vals, want) {
		t.Errorf("got %#v, want %#v", vals, want)
	}
}

func TestScreamingSnake(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Port", "PORT"},
		{"LatestSeason", "LATEST_SEASON"},
		{"DBPassword", "DB_PASSWORD"},
		{"HTTPPort", "HTTP_PORT"},
		{"APIKey", "API_KEY"},
		{"OAuth2Token", "O_AUTH2_TOKEN"},
		{"ID", "ID"},
		{"URL", "URL"},
		{"MaxIdleConns", "MAX_IDLE_CONNS"},
	}
	for _, tc := range tests {
		if got := screamingSnake(tc.in); got != tc.want {
			t.Errorf("screamingSnake(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripCommentBoundaries(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"empty value with comment", "A= # note\nB=2", map[string]string{"A": "", "B": "2"}},
		{"hex colour", "C=#0af", map[string]string{"C": "#0af"}},
		{"url fragment", "U=http://x/#frag", map[string]string{"U": "http://x/#frag"}},
		{"tab before comment", "A=v\t# note", map[string]string{"A": "v"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDotEnv(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}
