package envx

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// A comma separator has to survive the tag grammar, since the comma is also
// the option delimiter. Getting this wrong splits "a,b" into three items.
func TestSeparatorComma(t *testing.T) {
	type cfg struct {
		Explicit []string `env:"A,separator=,"`
		Implicit []string `env:"B"`
		Semi     []string `env:"C,separator=;"`
	}
	l, _ := New(Values(map[string]string{"A": "a,b", "B": "a,b", "C": "a;b"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b"}
	for name, have := range map[string][]string{
		"Explicit": got.Explicit,
		"Implicit": got.Implicit,
		"Semi":     got.Semi,
	} {
		if !slices.Equal(have, want) {
			t.Errorf("%s = %#v, want %#v", name, have, want)
		}
	}
}

func TestQuotedTagValueMayContainCommas(t *testing.T) {
	type cfg struct {
		Hosts []string `env:"HOSTS,default='a,b,c'"`
		Note  string   `env:"NOTE,default='hello, world'"`
	}
	l, _ := New(Values(nil))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(got.Hosts, want) {
		t.Errorf("Hosts = %#v, want %#v", got.Hosts, want)
	}
	if got.Note != "hello, world" {
		t.Errorf("Note = %q", got.Note)
	}
}

// An unrecognised option is reported rather than skipped. Skipping it would
// turn `default=a,b` into "a" with the rest silently dropped.
func TestUnknownTagOptionIsAnError(t *testing.T) {
	tests := []struct{ name, tag string }{
		{"unquoted comma in default", "HOSTS,default=a,b"},
		{"miscased notEmpty", "NAME,notempty"},
		{"typo", "NAME,requird"},
		{"unknown option", "NAME,minLength=3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseFieldTag(tc.tag); !errors.Is(err, ErrInvalidTag) {
				t.Errorf("parseFieldTag(%q) err = %v, want ErrInvalidTag", tc.tag, err)
			}
		})
	}
}

func TestValidTagOptionsParse(t *testing.T) {
	o, err := parseFieldTag("PORT,default=8080,required,notEmpty,separator=;")
	if err != nil {
		t.Fatal(err)
	}
	if o.name != "PORT" || o.def != "8080" || !o.hasDefault ||
		!o.required || !o.notEmpty || o.separator != ";" {
		t.Errorf("parsed = %+v", o)
	}
}

func TestInvalidTagSurfacesAsAFieldError(t *testing.T) {
	type cfg struct {
		Hosts []string `env:"HOSTS,default=a,b"`
	}
	l, _ := New(Values(nil))
	_, err := l.Load[cfg]()

	var e *Error
	if !errors.As(err, &e) || len(e.Fields) != 1 {
		t.Fatalf("want one field error, got %v", err)
	}
	if e.Fields[0].Key != "HOSTS" {
		t.Errorf("Key = %q, want HOSTS", e.Fields[0].Key)
	}
	if !errors.Is(err, ErrInvalidTag) {
		t.Error("errors.Is(err, ErrInvalidTag) = false")
	}
}

type embeddedBase struct {
	Region string `env:"REGION,default=eu-west-1"`
}

// An embedded struct's fields are promoted, so it adds no path segment. An
// embedded scalar is an ordinary leaf and keeps its name.
func TestEmbeddedFields(t *testing.T) {
	type Port int
	type cfg struct {
		embeddedBase
		Port
	}

	l, _ := New(Values(map[string]string{"PORT": "8080"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "eu-west-1" {
		t.Errorf("Region = %q; embedded struct fields should be promoted", got.Region)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d", got.Port)
	}

	l2, _ := New(Values(map[string]string{"PORT": "abc"}))
	_, err = l2.Load[cfg]()
	var e *Error
	if !errors.As(err, &e) || len(e.Fields) != 1 {
		t.Fatalf("want one field error, got %v", err)
	}
	if e.Fields[0].Field != "cfg.Port" {
		t.Errorf("Field = %q, want cfg.Port", e.Fields[0].Field)
	}
}

type unexportedPtrBase struct {
	Zone string `env:"ZONE"`
}

type embeddedScalar int

// reflect permits setting fields promoted from an embedded unexported struct,
// but not an embedded unexported pointer or scalar — writing to either panics.
// Walking must take the first and skip the other two.
func TestEmbeddedUnexportedTypes(t *testing.T) {
	type cfg struct {
		embeddedBase // settable through promotion
		*unexportedPtrBase
		embeddedScalar
		Normal string `env:"NORMAL"`
	}

	l, _ := New(Values(map[string]string{
		"REGION": "us-east-1",
		"ZONE":   "za",
		"NORMAL": "n",
	}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Region != "us-east-1" {
		t.Errorf("Region = %q; an embedded unexported struct promotes settable fields", got.Region)
	}
	if got.Normal != "n" {
		t.Errorf("Normal = %q", got.Normal)
	}
	if got.unexportedPtrBase != nil {
		t.Error("an embedded unexported pointer is not settable and must be left nil")
	}
	if got.embeddedScalar != 0 {
		t.Error("an embedded unexported scalar is not settable and must stay zero")
	}
}

func TestExpansionRespectsEscapedBackslashes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// One backslash escapes the $.
		{"escaped dollar", `BASE=/srv` + "\n" + `P="C:\${BASE}"`, `C:${BASE}`},
		// Two backslashes are an escaped backslash, so the $ is live.
		{"escaped backslash", `BASE=/srv` + "\n" + `P="C:\\${BASE}"`, `C:\/srv`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDotEnv(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got["P"] != tc.want {
				t.Errorf("P = %q, want %q", got["P"], tc.want)
			}
		})
	}
}

// A Loader is documented as a reusable snapshot, so concurrent loads of
// different structs must be safe.
func TestConcurrentLoadIsSafe(t *testing.T) {
	l, _ := New(Values(map[string]string{
		"PORT": "8080", "DB_HOST": "db.internal", "HOSTS": "a,b,c",
	}))

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			api, err := l.Load[apiConfig]()
			if err != nil || api.Port != 8080 || api.Timeout != 30*time.Second {
				t.Errorf("apiConfig = %+v, err = %v", api, err)
			}
			db, err := l.Load[databaseConfig]()
			if err != nil || db.Host != "db.internal" {
				t.Errorf("databaseConfig = %+v, err = %v", db, err)
			}
		})
	}
	wg.Wait()
}

func TestPointerAndTextUnmarshalerFields(t *testing.T) {
	type cfg struct {
		Port  *int      `env:"PORT"`
		Start time.Time `env:"START"`
		Ratio *float64  `env:"RATIO"`
		Raw   []byte    `env:"RAW"`
		Empty []string  `env:"EMPTY"`
	}
	l, _ := New(Values(map[string]string{
		"PORT":  "8080",
		"START": "2026-01-02T15:04:05Z",
		"RATIO": "0.25",
		"RAW":   "raw,bytes",
		"EMPTY": "",
	}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Port == nil || *got.Port != 8080 {
		t.Errorf("Port = %v", got.Port)
	}
	if !got.Start.Equal(time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)) {
		t.Errorf("Start = %v", got.Start)
	}
	if got.Ratio == nil || *got.Ratio != 0.25 {
		t.Errorf("Ratio = %v", got.Ratio)
	}
	// []byte is a payload, not a list, so the separator does not apply.
	if string(got.Raw) != "raw,bytes" {
		t.Errorf("Raw = %q", got.Raw)
	}
	if got.Empty == nil || len(got.Empty) != 0 {
		t.Errorf("Empty = %#v, want a zero-length slice", got.Empty)
	}
}

func TestIntegerRangeErrorNamesTheType(t *testing.T) {
	type cfg struct {
		Small int8 `env:"SMALL"`
	}
	l, _ := New(Values(map[string]string{"SMALL": "999"}))
	_, err := l.Load[cfg]()
	var e *Error
	if !errors.As(err, &e) || len(e.Fields) != 1 {
		t.Fatalf("want one field error, got %v", err)
	}
	if want := `SMALL: must fit in int8, got "999" (from values)`; e.Fields[0].Error() != want {
		t.Errorf("got %q, want %q", e.Fields[0].Error(), want)
	}
}

func TestUnsupportedTypeOnlyErrorsWhenSupplied(t *testing.T) {
	type cfg struct {
		M map[string]string `env:"M"`
	}
	l, _ := New(Values(nil))
	if _, err := l.Load[cfg](); err != nil {
		t.Errorf("an unsupported field nothing supplied should be left alone, got %v", err)
	}

	l2, _ := New(Values(map[string]string{"M": "a=b"}))
	if _, err := l2.Load[cfg](); !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("want ErrUnsupportedType, got %v", err)
	}
}
