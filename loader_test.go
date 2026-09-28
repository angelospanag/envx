package envx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPrecedenceEarlierSourceWins(t *testing.T) {
	l, err := New(
		NamedValues("first", map[string]string{"A": "from-first"}),
		NamedValues("second", map[string]string{"A": "from-second", "B": "only-second"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := l.lookup("A"); got != "from-first" {
		t.Errorf("A = %q, want from-first", got)
	}
	if got, _, _ := l.lookup("B"); got != "only-second" {
		t.Errorf("B = %q, want only-second", got)
	}
}

func TestOriginTracksTheSupplyingLayer(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, ".env.local")
	base := filepath.Join(dir, ".env")
	if err := os.WriteFile(local, []byte("PORT=9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte("PORT=8080\nHOST=example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := New(DotEnv(local, base))
	if err != nil {
		t.Fatal(err)
	}

	// DotEnv expands into one layer per file, so provenance names the exact
	// file rather than a generic "dotenv".
	if got, ok := l.Origin("PORT"); !ok || string(got) != local {
		t.Errorf("Origin(PORT) = %q, %v; want %q", got, ok, local)
	}
	if got, ok := l.Origin("HOST"); !ok || string(got) != base {
		t.Errorf("Origin(HOST) = %q, %v; want %q", got, ok, base)
	}
	if _, ok := l.Origin("ABSENT"); ok {
		t.Error("Origin(ABSENT) reported a layer for a key nothing supplied")
	}
	if want := []Origin{Origin(local), Origin(base)}; !slices.Equal(l.Layers(), want) {
		t.Errorf("Layers() = %v, want %v", l.Layers(), want)
	}
}

type apiConfig struct {
	Port    int           `env:"PORT,default=8080"`
	Timeout time.Duration `env:"TIMEOUT,default=30s"`
}

type databaseConfig struct {
	Host     string         `env:"DB_HOST,required"`
	Password Secret[string] `env:"DB_PASSWORD"`
	MaxConns int            `env:"DB_MAX_CONNS,default=10"`
}

// The flagship use: one resolved snapshot, several config structs, no repeated
// options and no second read of the files.
func TestOneLoaderManyStructs(t *testing.T) {
	l, err := New(Values(map[string]string{
		"PORT":        "9090",
		"DB_HOST":     "db.internal",
		"DB_PASSWORD": "hunter2",
	}))
	if err != nil {
		t.Fatal(err)
	}

	api, err := l.Load[apiConfig]()
	if err != nil {
		t.Fatalf("Load[apiConfig]: %v", err)
	}
	if api.Port != 9090 {
		t.Errorf("Port = %d, want 9090", api.Port)
	}
	if api.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", api.Timeout)
	}

	db, err := l.Load[databaseConfig]()
	if err != nil {
		t.Fatalf("Load[databaseConfig]: %v", err)
	}
	if db.Host != "db.internal" {
		t.Errorf("Host = %q", db.Host)
	}
	if db.Password.Get() != "hunter2" {
		t.Errorf("Password.Get() = %q", db.Password.Get())
	}
	if db.MaxConns != 10 {
		t.Errorf("MaxConns = %d, want the default 10", db.MaxConns)
	}
}

func TestErrorAggregationReportsEveryField(t *testing.T) {
	type cfg struct {
		Port    int           `env:"PORT"`
		Timeout time.Duration `env:"TIMEOUT"`
		Debug   bool          `env:"DEBUG"`
		APIKey  string        `env:"API_KEY,required"`
		Fine    string        `env:"FINE"`
	}

	l, err := New(NamedValues(".env.local", map[string]string{
		"PORT":    "abc",
		"TIMEOUT": "nope",
		"DEBUG":   "maybe",
		"FINE":    "ok",
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = l.Load[cfg]()
	if err == nil {
		t.Fatal("want an error")
	}

	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *envx.Error, got %T", err)
	}
	if len(e.Fields) != 4 {
		t.Fatalf("want 4 field errors, got %d:\n%s", len(e.Fields), err)
	}

	// The env var name leads every message; the Go field path is available but
	// is not what the reader has to act on.
	wantLines := []string{
		`PORT: must be an integer, got "abc" (from .env.local)`,
		`TIMEOUT: must be a duration such as 30s or 5m, got "nope" (from .env.local)`,
		`DEBUG: must be a boolean such as true or false, got "maybe" (from .env.local)`,
		`API_KEY: required but not set`,
	}
	for i, want := range wantLines {
		if got := e.Fields[i].Error(); got != want {
			t.Errorf("field %d:\n got %s\nwant %s", i, got, want)
		}
	}
	if !strings.HasPrefix(e.Error(), "envx: 4 problems loading cfg:") {
		t.Errorf("summary line wrong:\n%s", e.Error())
	}
	if !errors.Is(err, ErrMissing) {
		t.Error("errors.Is(err, ErrMissing) = false, want true")
	}
	if e.Fields[0].Field != "cfg.Port" {
		t.Errorf("Field = %q, want cfg.Port", e.Fields[0].Field)
	}
}

func TestSetButEmptyIsAPresentValue(t *testing.T) {
	type cfg struct {
		Name string `env:"NAME,default=fallback"`
		Port int    `env:"PORT,default=8080"`
	}

	// NAME= supplies the empty string, so the default does not apply.
	l, err := New(Values(map[string]string{"NAME": ""}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "" {
		t.Errorf("Name = %q, want the empty string that NAME= supplied", got.Name)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want the default 8080 since PORT was absent", got.Port)
	}

	// The same rule means an empty value fails to parse as an int, loudly.
	l2, _ := New(Values(map[string]string{"PORT": ""}))
	if _, err := l2.Load[cfg](); err == nil {
		t.Error("PORT= should fail to parse as an int, not fall back to the default")
	}
}

func TestNotEmpty(t *testing.T) {
	type cfg struct {
		Name string `env:"NAME,notEmpty,default=fallback"`
	}
	l, _ := New(Values(map[string]string{"NAME": ""}))
	_, err := l.Load[cfg]()
	if !errors.Is(err, ErrEmpty) {
		t.Errorf("want ErrEmpty, got %v", err)
	}
}

func TestNestedStructPrefix(t *testing.T) {
	type dbConf struct {
		Host string `env:"HOST,default=localhost"`
		Port int    `env:"PORT,default=5432"`
	}
	type cfg struct {
		Service string  `env:"SERVICE"`
		DB      dbConf  `envPrefix:"DB_"`
		Replica *dbConf `envPrefix:"REPLICA_"`
	}

	l, _ := New(Values(map[string]string{
		"SERVICE":      "api",
		"DB_HOST":      "primary.internal",
		"REPLICA_PORT": "5433",
	}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.DB.Host != "primary.internal" || got.DB.Port != 5432 {
		t.Errorf("DB = %+v", got.DB)
	}
	if got.Replica == nil || got.Replica.Host != "localhost" || got.Replica.Port != 5433 {
		t.Errorf("Replica = %+v", got.Replica)
	}
}

func TestNestedErrorPathAndKey(t *testing.T) {
	type dbConf struct {
		Port int `env:"PORT"`
	}
	type cfg struct {
		DB dbConf `envPrefix:"DB_"`
	}
	l, _ := New(Values(map[string]string{"DB_PORT": "x"}))
	_, err := l.Load[cfg]()

	var e *Error
	if !errors.As(err, &e) || len(e.Fields) != 1 {
		t.Fatalf("want one field error, got %v", err)
	}
	if e.Fields[0].Key != "DB_PORT" {
		t.Errorf("Key = %q, want DB_PORT", e.Fields[0].Key)
	}
	if e.Fields[0].Field != "cfg.DB.Port" {
		t.Errorf("Field = %q, want cfg.DB.Port", e.Fields[0].Field)
	}
}

type validatedConfig struct {
	DatabaseURL string `env:"DATABASE_URL"`
	DBHost      string `env:"DB_HOST"`
	DBName      string `env:"DB_NAME"`
}

func (c validatedConfig) Validate() error {
	if c.DatabaseURL == "" && (c.DBHost == "" || c.DBName == "") {
		return errors.New("set DATABASE_URL, or both DB_HOST and DB_NAME")
	}
	return nil
}

func TestValidateRunsAfterFieldsArePopulated(t *testing.T) {
	l, _ := New(Values(map[string]string{"DB_HOST": "only-host"}))
	_, err := l.Load[validatedConfig]()
	if err == nil {
		t.Fatal("want a cross-field validation error")
	}
	if !strings.Contains(err.Error(), "set DATABASE_URL, or both DB_HOST and DB_NAME") {
		t.Errorf("unexpected error: %v", err)
	}

	l2, _ := New(Values(map[string]string{"DATABASE_URL": "postgres://x"}))
	if _, err := l2.Load[validatedConfig](); err != nil {
		t.Errorf("want no error, got %v", err)
	}
}

func TestValidateIsSkippedWhenFieldsFailed(t *testing.T) {
	type cfg struct {
		Port int `env:"PORT"`
	}
	l, _ := New(Values(map[string]string{"PORT": "abc"}))
	_, err := l.Load[cfg]()

	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("got %T", err)
	}
	if e.Err != nil {
		t.Error("Validate should not run once a field has failed")
	}
}

func TestLoadRejectsNonStruct(t *testing.T) {
	l, _ := New()
	if _, err := l.Load[int](); err == nil {
		t.Error("want an error for a non-struct type parameter")
	}
}

func TestPackageLevelLoadUsesGivenSources(t *testing.T) {
	type cfg struct {
		Port int `env:"PORT"`
	}
	got, err := Load[cfg](WithSources(Values(map[string]string{"PORT": "1234"})))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d", got.Port)
	}
}

func TestDefaultSourcesPutProcessEnvFirst(t *testing.T) {
	// A checked-in .env must never override what the deployment actually set.
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("ENVX_TEST_PORT=1111\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENVX_TEST_PORT", "2222")

	type cfg struct {
		Port int `env:"ENVX_TEST_PORT"`
	}
	got, err := Load[cfg](WithSources(OSEnv(), DotEnv(envFile)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 2222 {
		t.Errorf("Port = %d, want 2222 from the process environment", got.Port)
	}
}

func TestMustPanicsOnBadConfig(t *testing.T) {
	type cfg struct {
		Port int `env:"PORT,required"`
	}
	defer func() {
		if recover() == nil {
			t.Error("Must should panic when the config is invalid")
		}
	}()
	l, _ := New()
	_ = l.Must[cfg]()
}

func TestKeys(t *testing.T) {
	l, _ := New(Values(map[string]string{"B": "2", "A": "1", "C": "3"}))
	if want := []string{"A", "B", "C"}; !slices.Equal(l.Keys(), want) {
		t.Errorf("Keys() = %v, want %v", l.Keys(), want)
	}
}

func TestSourceErrorsPropagate(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, ".env")
	if err := os.WriteFile(bad, []byte("this is not valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(DotEnv(bad)); err == nil {
		t.Error("a malformed .env should fail the New call")
	} else if !strings.Contains(err.Error(), bad) {
		t.Errorf("error should name the file, got %v", err)
	}
}

func ExampleLoader_Load() {
	l, _ := New(NamedValues("example", map[string]string{
		"PORT":    "9090",
		"DB_HOST": "db.internal",
	}))

	api, _ := l.Load[apiConfig]()
	db, _ := l.Load[databaseConfig]()

	fmt.Println(api.Port, api.Timeout)
	fmt.Println(db.Host, db.MaxConns)
	// Output:
	// 9090 30s
	// db.internal 10
}
