package envx

import (
	"errors"
	"strings"
	"testing"
)

// FuzzParseDotEnv checks that no input can panic the parser, and that anything
// it accepts satisfies the invariants the rest of the library relies on.
func FuzzParseDotEnv(f *testing.F) {
	seeds := []string{
		"A=1\nB=two",
		"export A=1",
		`A="quoted"`,
		`A='literal'`,
		"A=\"multi\nline\"",
		"BASE=/srv\nP=${BASE}/a",
		"P=${NOPE:-fallback}",
		"A=v # comment",
		"C=#0af",
		"A=",
		`A="\n\t\\\""`,
		`P=\$NOTAVAR`,
		"A=1\r\nB=2",
		"${",
		"=",
		"A=$",
		"A=${",
		"A=${}",
		"A='",
		`A="`,
		strings.Repeat("A=${A}\n", 50),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		got, err := parseDotEnv(in)
		if err != nil {
			if got != nil {
				t.Errorf("parseDotEnv returned both a map and an error %v", err)
			}
			return
		}
		for k := range got {
			// Every accepted key must be a usable environment variable name,
			// since keys are looked up verbatim across every source.
			if err := checkKey(k); err != nil {
				t.Errorf("accepted an invalid key %q: %v", k, err)
			}
			if strings.ContainsAny(k, "\n\r") {
				t.Errorf("key %q contains a newline", k)
			}
		}
	})
}

// FuzzParseFieldTag checks the struct-tag grammar. A tag comes from source
// code rather than input, so the bar is that it never panics and never
// silently returns a broken separator.
func FuzzParseFieldTag(f *testing.F) {
	seeds := []string{
		"PORT",
		"PORT,default=8080",
		"PORT,required,notEmpty",
		"HOSTS,separator=;",
		"HOSTS,separator=,",
		"HOSTS,default='a,b'",
		"NAME,default='unclosed",
		",required",
		"",
		",",
		",,,",
		"A,default=",
		"A,default='',required",
		"A,'",
		"A,separator=''",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, tag string) {
		o, err := parseFieldTag(tag)
		if err != nil {
			return
		}
		// An empty separator would split a value into single characters, so it
		// must never survive parsing.
		if o.separator == "" {
			t.Errorf("parseFieldTag(%q) produced an empty separator", tag)
		}
	})
}

// FuzzLoadRoundTrip runs a fuzzed .env through the whole pipeline, so that a
// parser quirk cannot crash the decoder.
func FuzzLoadRoundTrip(f *testing.F) {
	f.Add("PORT=8080\nNAME=x\nHOSTS=a,b\nTIMEOUT=30s\nDEBUG=true")
	f.Add("PORT=\nNAME=\nHOSTS=\nTIMEOUT=\nDEBUG=")
	f.Add("PORT=abc\nTIMEOUT=nope\nDEBUG=maybe")

	type cfg struct {
		Port   int            `env:"PORT,default=8080"`
		Name   string         `env:"NAME"`
		Hosts  []string       `env:"HOSTS"`
		Secret Secret[string] `env:"SECRET"`
	}

	f.Fuzz(func(t *testing.T, in string) {
		vals, err := parseDotEnv(in)
		if err != nil {
			return
		}
		l, err := New(Values(vals))
		if err != nil {
			t.Fatalf("New failed on parsed values: %v", err)
		}
		got, err := l.Load[cfg]()
		if err != nil {
			// Every failure must be an *Error carrying at least one cause.
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("want *Error, got %T: %v", err, err)
			}
			if len(e.Fields) == 0 && e.Err == nil {
				t.Fatal("*Error carried no causes")
			}
			// The message must never contain a secret's plaintext.
			if s, ok := vals["SECRET"]; ok && s != "" && strings.Contains(e.Error(), s) {
				t.Errorf("error output leaked the secret %q:\n%v", s, e)
			}
			return
		}
		_ = got
	})
}
