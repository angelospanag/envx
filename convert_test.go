package envx

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// NaN and Inf parse happily under strconv but are never a deliberate config
// value, and they fail silently: every comparison against NaN is false, so a
// threshold quietly stops working.
func TestFloatRejectsNaNAndInf(t *testing.T) {
	type cfg struct {
		R float64 `env:"R"`
	}
	for _, raw := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "infinity"} {
		l, _ := New(Values(map[string]string{"R": raw}))
		_, err := l.Load[cfg]()
		if err == nil {
			t.Errorf("R=%q was accepted; want a finite-number error", raw)
			continue
		}
		if !strings.Contains(err.Error(), "must be a finite number") {
			t.Errorf("R=%q: %v", raw, err)
		}
	}

	// Ordinary values, including exponent and hex-float forms, still parse.
	for raw, want := range map[string]float64{"1.5": 1.5, "1e5": 100000, "0x1p-2": 0.25, "-3": -3} {
		l, _ := New(Values(map[string]string{"R": raw}))
		got, err := l.Load[cfg]()
		if err != nil || got.R != want {
			t.Errorf("R=%q: got %v err=%v, want %v", raw, got.R, err, want)
		}
	}
}

// A trailing or doubled separator is the commonest way to mistype a list, and
// an empty item would become an empty host, path or token that only fails
// much later.
func TestSliceSkipsEmptyItems(t *testing.T) {
	type cfg struct {
		S []string `env:"S"`
	}
	tests := map[string][]string{
		"a,b":     {"a", "b"},
		"a,b,":    {"a", "b"},
		",a":      {"a"},
		"a,,b":    {"a", "b"},
		" a , b ": {"a", "b"},
		",":       {},
		"":        {},
	}
	for raw, want := range tests {
		l, _ := New(Values(map[string]string{"S": raw}))
		got, err := l.Load[cfg]()
		if err != nil {
			t.Errorf("S=%q: %v", raw, err)
			continue
		}
		if !slices.Equal(got.S, want) {
			t.Errorf("S=%q: got %#v, want %#v", raw, got.S, want)
		}
	}
}

func TestSliceOfNumbersSkipsEmptyItems(t *testing.T) {
	type cfg struct {
		Ports []int `env:"PORTS"`
	}
	l, _ := New(Values(map[string]string{"PORTS": "80,443,"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{80, 443}; !slices.Equal(got.Ports, want) {
		t.Errorf("got %#v, want %#v", got.Ports, want)
	}
}

// A bad item is still reported, and numbered by its position in the original
// list rather than after empties are dropped.
func TestSliceReportsBadItem(t *testing.T) {
	type cfg struct {
		Ports []int `env:"PORTS"`
	}
	l, _ := New(Values(map[string]string{"PORTS": "80,nope,443"}))
	_, err := l.Load[cfg]()
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "item 2") {
		t.Errorf("error should name the failing item: %v", err)
	}
}

// Values that Go's parsers accept but which would surprise in a config file.
func TestNumericParsingIsStrict(t *testing.T) {
	type cfg struct {
		N int           `env:"N"`
		D time.Duration `env:"D"`
	}
	for _, raw := range []string{" 8080", "8080 ", "1_000", "0x10", "abc", ""} {
		l, _ := New(Values(map[string]string{"N": raw}))
		if _, err := l.Load[cfg](); err == nil {
			t.Errorf("N=%q was accepted", raw)
		}
	}
	// A duration must carry a unit; a bare number is rejected.
	l, _ := New(Values(map[string]string{"D": "5"}))
	if _, err := l.Load[cfg](); err == nil {
		t.Error("D=5 was accepted; a duration needs a unit")
	}
	l2, _ := New(Values(map[string]string{"D": "1h30m"}))
	got, err := l2.Load[cfg]()
	if err != nil || got.D != 90*time.Minute {
		t.Errorf("D=1h30m: got %v err=%v", got.D, err)
	}
}

// strconv.ParseBool is the accepted set; yes/on/enabled are rejected with a
// message that says what to write instead.
func TestBoolAcceptsOnlyStrconvForms(t *testing.T) {
	type cfg struct {
		B bool `env:"B"`
	}
	for _, raw := range []string{"true", "TRUE", "True", "1", "t"} {
		l, _ := New(Values(map[string]string{"B": raw}))
		got, err := l.Load[cfg]()
		if err != nil || !got.B {
			t.Errorf("B=%q: got %v err=%v", raw, got.B, err)
		}
	}
	for _, raw := range []string{"yes", "on", "Y", "enabled"} {
		l, _ := New(Values(map[string]string{"B": raw}))
		_, err := l.Load[cfg]()
		if err == nil {
			t.Errorf("B=%q was accepted", raw)
			continue
		}
		if !strings.Contains(err.Error(), "true or false") {
			t.Errorf("B=%q should say what to write instead: %v", raw, err)
		}
	}
}

func TestScreamingSnakeAcronymRuns(t *testing.T) {
	for in, want := range map[string]string{
		"MyURL":       "MY_URL",
		"URLPath":     "URL_PATH",
		"A":           "A",
		"ID2":         "ID2",
		"HTTP2Server": "HTTP2_SERVER",
		"Xy":          "XY",
	} {
		if got := screamingSnake(in); got != want {
			t.Errorf("screamingSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

// []byte is a payload, so a separator inside it is data, not a delimiter.
func TestByteSliceIsNotSplit(t *testing.T) {
	type cfg struct {
		Raw []byte `env:"RAW"`
	}
	l, _ := New(Values(map[string]string{"RAW": "a,b,"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Raw) != "a,b," {
		t.Errorf("Raw = %q, want the value unchanged", got.Raw)
	}
}

func TestUnsupportedStructInSliceIsReported(t *testing.T) {
	type inner struct{ X string }
	type cfg struct {
		Items []inner `env:"ITEMS"`
	}
	l, _ := New(Values(map[string]string{"ITEMS": "a,b"}))
	if _, err := l.Load[cfg](); !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("want ErrUnsupportedType, got %v", err)
	}
}
