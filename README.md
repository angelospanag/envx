# envx

[![Go Reference](https://pkg.go.dev/badge/github.com/angelospanag/envx.svg)](https://pkg.go.dev/github.com/angelospanag/envx)

Layered environment configuration for Go, with typed parsing, secret redaction
and error messages that name the variable you actually have to fix.

It replaces the dotenv loader, the tag-based parser and the hand-written
`validate()` that most Go services wire together and then copy between repos.

**Zero dependencies.**

```go
type Config struct {
	Port        int                 `env:"PORT,default=8080"`
	DatabaseURL string              `env:"DATABASE_URL,required"`
	Password    envx.Secret[string] `env:"DB_PASSWORD"`
	Timeout     time.Duration       `env:"TIMEOUT,default=30s"`
}

cfg, err := envx.Load[Config]()
```

## Why

Three things that existing libraries leave to you:

**Every problem at once.** A misconfigured service should tell you everything
that is wrong in one run, not make you fix one variable, redeploy, and discover
the next.

```
envx: 4 problems loading Config:
  PORT: must be an integer, got "abc" (from .env.local)
  DATABASE_URL: required but not set
  TIMEOUT: must be a duration such as 30s or 5m, got "1 fortnight" (from .env.local)
  DEBUG: must be a boolean such as true or false, got "yes please" (from .env.local)
```

The environment variable name leads every line, and the layer that supplied the
bad value is named — so each message points at the thing you have to go and
change, rather than at a Go field path and the name of a struct tag.

**Secrets that do not leak.** `envx.Secret[T]` redacts itself in `String`,
`GoString`, `Format`, `MarshalText`, `MarshalJSON` and `slog` output. A plain
`DBPassword string` field ends up in a log line the first time someone `%+v`s
the config.

```go
fmt.Printf("%+v\n", cfg)
// {Port:9090 DatabaseURL:postgres://localhost/app Password:[redacted] Timeout:30s}

cfg.Password.Get() // "hunter2" — the one deliberate way back
```

**Provenance.** The most common configuration question is "where did this value
come from?"

```go
origin, _ := l.Origin("PORT")
fmt.Println(origin) // ".env.local"
```

## Install

```sh
go get github.com/angelospanag/envx
```

Requires **Go 1.27+**. See [Go version](#go-version).

## Usage

### One struct

`Load` reads the process environment, then `.env`:

```go
cfg, err := envx.Load[Config]()
```

Point it somewhere else with `WithSources`:

```go
cfg, err := envx.Load[Config](envx.WithSources(
	envx.OSEnv(),
	envx.DotEnv(".env.local", ".env"),
	envx.SecretsDir("/run/secrets"),
))
```

### Many structs, one snapshot

Build a `Loader` when several config structs share the same layers. The files
are read once and the merged snapshot serves every load:

```go
l, err := envx.New(
	envx.OSEnv(),
	envx.DotEnv(".env.local", ".env"),
	envx.SecretsDir("/run/secrets"),
)

api, err := l.Load[APIConfig]()
db,  err := l.Load[DatabaseConfig]()
```

`Load` is a generic *method*, which Go 1.27 made possible. Before it you had to
choose between a generic type (`Loader[T]`, locking one loader to one struct)
or a package-level function reading inside-out.

In `main`, where a bad config should stop the process, use `Must`:

```go
cfg := envx.Must[Config]()
```

## Sources

Sources are the extension point. Precedence is the order of the slice —
**earlier sources win** — not hardcoded logic.

| Source | Reads |
|---|---|
| `envx.OSEnv()` | the process environment |
| `envx.DotEnv(paths...)` | `.env` files; each path is its own layer, missing files are fine |
| `envx.SecretsDir(dir)` | file-mounted secrets; filename is the key, contents the value |
| `envx.Values(m)` | an in-memory map, for tests |
| `envx.NamedValues(name, m)` | the same, with a label for provenance |

Implement `Source` for anything else — Vault, AWS Secrets Manager, a database:

```go
type Source interface {
	Name() string                     // labels the layer in errors and provenance
	Values() (map[string]string, error)
}
```

### .env files never reach the process environment

`envx` parses `.env` into a map and leaves it there. Nothing is ever written
into `os.Environ`. That is what makes a `.env` file a distinct layer with its
own provenance, rather than something indistinguishable from the real
environment once it has been merged in.

Supported syntax:

```sh
KEY=value              # trailing comments need whitespace before the #
KEY="quoted value"     # \n \r \t \\ \" \$ escapes, may span lines
KEY='literal value'    # no escapes, no expansion, may span lines
export KEY=value       # the export prefix is ignored
KEY=                   # present, and empty — see below
COLOUR=#0af            # a # with no space before it is part of the value
BASE=/srv
PATH_A=${BASE}/a       # ${VAR}, $VAR and ${VAR:-default}
```

Expansion resolves against keys defined **earlier in the same file** only. It
never reaches into other layers or the process environment, so a `.env` file
always reads the same way on its own.

## Struct tags

```go
type Config struct {
	Port    int      `env:"PORT,default=8080"`
	URL     string   `env:"DATABASE_URL,required"`
	Name    string   `env:"NAME,notEmpty"`
	Hosts   []string `env:"HOSTS,separator=;"`
	Ignored string   `env:"-"`
	Derived string   // no tag: the key is DERIVED
}
```

| Option | Effect |
|---|---|
| `default=X` | used only when no source supplied the key at all |
| `required` | fails with `ErrMissing` when no source supplied the key |
| `notEmpty` | fails with `ErrEmpty` when the value is the empty string |
| `separator=;` | element separator for slices (default `,`) |
| `-` | skip the field |

Options are comma-separated, so a value that itself contains a comma must be
single-quoted:

```go
Hosts []string `env:"HOSTS,default='a,b,c'"`
```

An unrecognised option is an error, not something quietly ignored — that is
what makes the unquoted `default=a,b` above fail loudly instead of silently
becoming `"a"`, and what catches a miscased `notempty`.

Without a tag name, the key is the field name in `SCREAMING_SNAKE_CASE`, with
acronym runs kept together: `DBPassword` → `DB_PASSWORD`, `HTTPPort` →
`HTTP_PORT`.

Nested structs take a prefix:

```go
type Config struct {
	DB      DBConfig  `envPrefix:"DB_"`       // DB_HOST, DB_PORT
	Replica *DBConfig `envPrefix:"REPLICA_"`  // REPLICA_HOST, REPLICA_PORT
}
```

A **value** group is always populated, defaults included. A **pointer** group
is optional: it stays `nil` unless some source supplied at least one key with
its prefix, and a `required` field inside an absent group is not a failure.
That is how a config says "this section may not be there at all".

### Supported types

`string`, `bool`, all sized `int`/`uint`/`float` types, `time.Duration`, slices
of any of those, `[]byte`, pointers to any of those, and **anything
implementing `encoding.TextUnmarshaler`** — which covers `time.Time`,
`netip.Addr`, `net.IP` and your own types.

## Cross-field rules

Tags cover a single field. For rules that span fields, implement `Validate`:

```go
func (c Config) Validate() error {
	if c.DatabaseURL == "" && (c.DBHost == "" || c.DBName == "") {
		return errors.New("set DATABASE_URL, or both DB_HOST and DB_NAME")
	}
	return nil
}
```

It runs once, after every field is populated, and only if no field failed —
there is no point range-checking a port that did not parse.

This interface is deliberately non-generic. Generic methods are excluded from
runtime method sets, so a generic `Validate` would be invisible to the
reflection this library is built on.

## Errors

`Load` always returns an `*envx.Error` on failure:

```go
var e *envx.Error
if errors.As(err, &e) {
	for _, f := range e.Fields {
		fmt.Println(f.Key, f.Field, f.Origin, f.Err)
	}
}
```

`Error.Unwrap() []error` exposes every cause, so `errors.Is` works against the
whole set:

```go
if errors.Is(err, envx.ErrMissing) { /* something required was not set */ }
```

Sentinels: `ErrMissing`, `ErrEmpty`, `ErrUnsupportedType`, `ErrInvalidTag`.

Raw values are redacted in error output when the field is a `Secret`.

## Set but empty

`PORT=` supplies the empty string. It does not leave `PORT` unset.

A `default` therefore does not apply to it, and it fails to parse as an `int`
exactly as `"abc"` would. Shells, dotenv files and Kubernetes all disagree on
this, so it is worth being explicit: this is the only choice that stays
recoverable. A field that genuinely wants an empty string has no other way to
say so, whereas a field that wants empty-means-absent can say that with
`notEmpty` and a `default`.

## Precedence

Earlier sources win. The default order for `envx.Load` is the process
environment **first**, then `.env` — so a value baked into a checked-in `.env`
can never silently override what the deployment actually set.

If you want the opposite for local development, say so explicitly:

```go
envx.WithSources(envx.DotEnv(".env.local"), envx.OSEnv())
```

## Go version

`envx` requires Go 1.27, for generic methods. This is a deliberate first-mover
position rather than an oversight: the one-snapshot-many-structs loader is the
reason the library exists in this shape, and it was not expressible before.

## Scope

This is a 12-factor environment loader, not a config framework.

**Non-goals:** YAML/TOML/JSON config files, CLI flag parsing, live reload,
service discovery. The moment those appear it stops being a focused
environment loader and turns into a general configuration framework, and the
pitch stops being clear.

## Roadmap

Implemented: sources and layering, provenance, typed decoding, error
aggregation, `Secret[T]`, cross-field `Validate`.

Not yet built:

- `validate:"min=1,max=65535,oneof=..."` field rules — a small built-in rule
  set, so the module stays free of requires
- strict mode for unknown keys, to catch the `DB_PASSOWRD` typo that otherwise
  silently hands you the default password
- `.env.example` generation from the struct

## License

MIT
