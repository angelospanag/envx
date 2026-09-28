package envx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Origin labels the layer that supplied a value, such as ".env.local" or
// "OS environment".
type Origin string

// Source yields one layer of raw values. Sources never touch os.Environ: a
// .env file is read into a map and stays there, which is what makes layering
// and provenance possible.
type Source interface {
	// Name labels the source in provenance and error output.
	Name() string
	// Values returns this layer's raw key/value pairs.
	Values() (map[string]string, error)
}

// multiSource is implemented by sources expanding into several named layers,
// so DotEnv(".env.local", ".env") attributes each key to its own file.
type multiSource interface {
	expand() []Source
}

// --- OS environment ---------------------------------------------------------

type osEnvSource struct{}

// OSEnv reads the process environment.
func OSEnv() Source { return osEnvSource{} }

func (osEnvSource) Name() string { return "OS environment" }

func (osEnvSource) Values() (map[string]string, error) {
	env := os.Environ()
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out, nil
}

// --- Literal values ---------------------------------------------------------

type valuesSource struct {
	name string
	m    map[string]string
}

// Values returns a source backed by an in-memory map. Intended for tests.
func Values(m map[string]string) Source {
	return valuesSource{name: "values", m: m}
}

// NamedValues is Values with an explicit label for provenance output.
func NamedValues(name string, m map[string]string) Source {
	return valuesSource{name: name, m: m}
}

func (s valuesSource) Name() string { return s.name }

func (s valuesSource) Values() (map[string]string, error) {
	out := make(map[string]string, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}

// --- .env files -------------------------------------------------------------

type dotEnvSource struct{ path string }

// DotEnv reads one or more .env files into a map, never into the process
// environment. Each path is its own layer, so earlier paths win and provenance
// names the exact file. A missing file is fine; a malformed one is an error.
func DotEnv(paths ...string) Source {
	srcs := make([]Source, len(paths))
	for i, p := range paths {
		srcs[i] = dotEnvSource{path: p}
	}
	return sourceList(srcs)
}

func (s dotEnvSource) Name() string { return s.path }

func (s dotEnvSource) Values() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseDotEnv(string(data))
}

// sourceList is a flat group of sources that New expands in place.
type sourceList []Source

func (l sourceList) expand() []Source { return l }

func (l sourceList) Name() string {
	names := make([]string, len(l))
	for i, s := range l {
		names[i] = s.Name()
	}
	return strings.Join(names, ", ")
}

func (l sourceList) Values() (map[string]string, error) {
	out := map[string]string{}
	for _, s := range l {
		v, err := s.Values()
		if err != nil {
			return nil, err
		}
		for k, val := range v {
			if _, seen := out[k]; !seen {
				out[k] = val
			}
		}
	}
	return out, nil
}

// --- Secret files -----------------------------------------------------------

type secretsDirSource struct{ dir string }

// SecretsDir reads file-mounted secrets, as produced by Docker and Kubernetes
// volume mounts: each filename is a key, its contents the value, with one
// trailing newline removed. Dotfiles and non-regular files are skipped, and a
// missing directory is not an error.
func SecretsDir(dir string) Source { return secretsDirSource{dir: dir} }

func (s secretsDirSource) Name() string { return s.dir }

func (s secretsDirSource) Values() (map[string]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		// Stat rather than trust the entry: secrets are mounted as symlinks and
		// DirEntry.IsDir reports the link's own type, so a link to a directory
		// would be read as a file and fail the whole load. Broken links are
		// skipped too; a required field reports an absent secret far better.
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		out[e.Name()] = strings.TrimSuffix(string(data), "\n")
	}
	return out, nil
}
