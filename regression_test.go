package envx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type leafConf struct {
	Host string `env:"HOST,default=localhost"`
}

type midConf struct {
	Leaf leafConf `envPrefix:"LEAF_"`
}

// Cycle detection tracks the ancestor chain, not every type seen. Two fields
// of the same type are siblings, not a cycle — a seen-set would wrongly reject
// the second one.
func TestSameTypeTwiceIsNotACycle(t *testing.T) {
	type cfg struct {
		A leafConf `envPrefix:"A_"`
		B leafConf `envPrefix:"B_"`
	}
	l, _ := New(Values(map[string]string{"A_HOST": "a", "B_HOST": "b"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.A.Host != "a" || got.B.Host != "b" {
		t.Errorf("A=%q B=%q", got.A.Host, got.B.Host)
	}
}

func TestSameTypeAtDifferentDepths(t *testing.T) {
	type cfg struct {
		Mid  midConf  `envPrefix:"M_"`
		Leaf leafConf `envPrefix:"L_"`
	}
	l, _ := New(Values(map[string]string{"M_LEAF_HOST": "deep", "L_HOST": "shallow"}))
	got, err := l.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Mid.Leaf.Host != "deep" || got.Leaf.Host != "shallow" {
		t.Errorf("deep=%q shallow=%q", got.Mid.Leaf.Host, got.Leaf.Host)
	}
}

// Kubernetes and Docker mount secrets as symlinks, so SecretsDir has to
// resolve them — and must not choke on a link that points at a directory.
func TestSecretsDirFollowsSymlinks(t *testing.T) {
	t.Run("kubernetes projected volume", func(t *testing.T) {
		dir := t.TempDir()
		data := filepath.Join(dir, "..2026_09_08_11_00_00.123")
		if err := os.MkdirAll(data, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(data, "DB_PASSWORD"), "k8s-secret\n")
		symlink(t, data, filepath.Join(dir, "..data"))
		symlink(t, filepath.Join(dir, "..data", "DB_PASSWORD"), filepath.Join(dir, "DB_PASSWORD"))

		vals, err := SecretsDir(dir).Values()
		if err != nil {
			t.Fatal(err)
		}
		if vals["DB_PASSWORD"] != "k8s-secret" {
			t.Errorf("got %#v", vals)
		}
		if len(vals) != 1 {
			t.Errorf("dot-prefixed entries should be skipped, got %#v", vals)
		}
	})

	t.Run("symlink to a directory is skipped", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "realdir")
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "TOKEN"), "tok")
		symlink(t, sub, filepath.Join(dir, "linkdir"))

		vals, err := SecretsDir(dir).Values()
		if err != nil {
			t.Fatalf("a symlinked directory must not fail the load: %v", err)
		}
		if vals["TOKEN"] != "tok" || len(vals) != 1 {
			t.Errorf("got %#v", vals)
		}
	})

	t.Run("broken symlink is skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "TOKEN"), "tok")
		symlink(t, filepath.Join(dir, "nonexistent"), filepath.Join(dir, "DANGLING"))

		vals, err := SecretsDir(dir).Values()
		if err != nil {
			t.Fatalf("a broken symlink must not fail the load: %v", err)
		}
		if _, ok := vals["DANGLING"]; ok {
			t.Errorf("broken symlink should not appear, got %#v", vals)
		}
	})
}

// A failed load returns the zero value, never a partially populated struct.
func TestLoadReturnsZeroValueOnError(t *testing.T) {
	type cfg struct {
		Good string `env:"GOOD"`
		Bad  int    `env:"BAD"`
	}
	l, _ := New(Values(map[string]string{"GOOD": "ok", "BAD": "abc"}))
	got, err := l.Load[cfg]()
	if err == nil {
		t.Fatal("want an error")
	}
	if got.Good != "" || got.Bad != 0 {
		t.Errorf("got %+v; a failed load must not return partial values", got)
	}
}

func TestValidateFailureAlsoReturnsZeroValue(t *testing.T) {
	l, _ := New(Values(map[string]string{"DB_HOST": "only-host"}))
	got, err := l.Load[validatedConfig]()
	if err == nil {
		t.Fatal("want a validation error")
	}
	if got.DBHost != "" {
		t.Errorf("got %+v; a failed validation must not return populated values", got)
	}
}

// An optional group needs a prefix to be optional. Without one it shares its
// parent's namespace, so it is always populated rather than depending on
// whether unrelated keys happen to exist.
func TestPointerGroupWithoutPrefixIsAlwaysPopulated(t *testing.T) {
	type cfg struct {
		Unrelated string `env:"UNRELATED"`
		Group     *leafConf
	}
	for name, src := range map[string]Source{
		"with unrelated keys": Values(map[string]string{"UNRELATED": "x"}),
		"empty snapshot":      Values(nil),
	} {
		l, _ := New(src)
		got, err := l.Load[cfg]()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Group == nil || got.Group.Host != "localhost" {
			t.Errorf("%s: Group = %v, want it populated with defaults", name, got.Group)
		}
	}
}

func TestLoadRejectsPointerTypeParameter(t *testing.T) {
	l, _ := New()
	if _, err := l.Load[*leafConf](); err == nil {
		t.Error("want an error for a pointer type parameter")
	} else if errors.As(err, new(*Error)) {
		t.Error("a misuse of the type parameter should not be reported as a field error")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
