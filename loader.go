package envx

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Validator is implemented by config structs needing cross-field rules, such
// as "if DATABASE_URL is empty then DB_HOST and DB_NAME must both be set". It
// runs once every field is populated, and only if none failed.
//
// Non-generic on purpose: generic methods are excluded from runtime method
// sets, so a generic Validate would be invisible to reflection.
type Validator interface {
	Validate() error
}

// entry is one resolved value and where it came from.
type entry struct {
	value  string
	origin Origin
}

// Loader holds a merged snapshot of every source, read once at construction.
// Earlier sources win, and one Loader can populate any number of structs.
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

// hasPrefix reports whether any key starts with prefix, deciding whether an
// optional pointer group was configured. Never called with an empty prefix.
func (l *Loader) hasPrefix(prefix string) bool {
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

// Load populates a T from the snapshot, reporting every field that fails
// rather than only the first. The returned error is always an [Error].
//
// A generic method, so one snapshot serves many structs:
//
//	api, err := l.Load[APIConfig]()
//	db, err := l.Load[DatabaseConfig]()
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

	// Return the zero value, never a half-filled struct: a port that failed to
	// parse would otherwise read as 0 rather than as obviously unset.
	var zero T
	if len(d.errs) > 0 {
		return zero, &Error{Type: name, Fields: d.errs}
	}

	if v, ok := any(&cfg).(Validator); ok { // pointer covers both receivers
		if err := v.Validate(); err != nil {
			return zero, &Error{Type: name, Err: err}
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
