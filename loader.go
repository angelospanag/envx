package envx

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Validator is implemented by config structs that need cross-field rules —
// the checks that no single field can express on its own, such as "if
// DATABASE_URL is empty then DB_HOST and DB_NAME must both be set".
//
// It runs once, after every field has been populated, and only if no field
// failed. This interface is deliberately non-generic: generic methods are
// excluded from runtime method sets, so a generic Validate would be invisible
// to the reflection this library is built on.
type Validator interface {
	Validate() error
}

// entry is one resolved value and where it came from.
type entry struct {
	value  string
	origin Origin
}

// Loader holds a resolved, merged snapshot of every source.
//
// Sources are read once, when the Loader is built. Earlier sources win over
// later ones. One Loader can populate any number of different config structs
// from that single snapshot.
type Loader struct {
	merged map[string]entry
	layers []Origin
}

// New reads every source and merges them into one snapshot. Earlier sources
// win over later ones.
func New(sources ...Source) (*Loader, error) {
	flat := make([]Source, 0, len(sources))
	for _, s := range sources {
		if ms, ok := s.(multiSource); ok {
			flat = append(flat, ms.expand()...)
			continue
		}
		flat = append(flat, s)
	}

	l := &Loader{merged: map[string]entry{}, layers: make([]Origin, 0, len(flat))}
	for _, s := range flat {
		vals, err := s.Values()
		if err != nil {
			return nil, err
		}
		origin := Origin(s.Name())
		l.layers = append(l.layers, origin)
		for k, v := range vals {
			if _, taken := l.merged[k]; !taken {
				l.merged[k] = entry{value: v, origin: origin}
			}
		}
	}
	return l, nil
}

func (l *Loader) lookup(key string) (string, Origin, bool) {
	e, ok := l.merged[key]
	return e.value, e.origin, ok
}

// hasPrefix reports whether any key in the snapshot starts with prefix. It
// decides whether an optional pointer group was configured at all.
func (l *Loader) hasPrefix(prefix string) bool {
	if prefix == "" {
		return len(l.merged) > 0
	}
	for k := range l.merged {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// Origin reports which layer supplied key.
func (l *Loader) Origin(key string) (Origin, bool) {
	e, ok := l.merged[key]
	return e.origin, ok
}

// Layers lists the resolved sources in precedence order.
func (l *Loader) Layers() []Origin { return slices.Clone(l.layers) }

// Keys lists every key in the merged snapshot, sorted.
func (l *Loader) Keys() []string {
	out := make([]string, 0, len(l.merged))
	for k := range l.merged {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Load populates a T from the snapshot.
//
// This is a generic method, which Go 1.27 made possible. Before it, a loader
// had to be either a generic type — locking one set of resolved sources to one
// config struct — or a package-level function reading inside-out. Now one
// snapshot serves many structs:
//
//	l, err := envx.New(envx.OSEnv(), envx.DotEnv(".env"))
//	api, err := l.Load[APIConfig]()
//	db, err := l.Load[DatabaseConfig]()
//
// Every field that fails is reported, not just the first. The returned error
// is always an *Error.
func (l *Loader) Load[T any]() (T, error) {
	var cfg T
	rv := reflect.ValueOf(&cfg).Elem()
	if rv.Kind() != reflect.Struct {
		return cfg, fmt.Errorf("envx: Load requires a struct type, got %s", rv.Type())
	}
	name := rv.Type().Name()
	if name == "" {
		name = "config"
	}

	d := &decoder{l: l}
	d.walk(rv, "", name)
	if len(d.errs) > 0 {
		return cfg, &Error{Type: name, Fields: d.errs}
	}

	// Checking the pointer covers both value and pointer receivers.
	if v, ok := any(&cfg).(Validator); ok {
		if err := v.Validate(); err != nil {
			return cfg, &Error{Type: name, Err: err}
		}
	}
	return cfg, nil
}

// Must is Load for main, where a bad config should stop the process.
func (l *Loader) Must[T any]() T {
	cfg, err := l.Load[T]()
	if err != nil {
		panic(err)
	}
	return cfg
}
