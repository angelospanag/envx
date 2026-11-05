# envx

[![Go Reference](https://pkg.go.dev/badge/github.com/angelospanag/envx.svg)](https://pkg.go.dev/github.com/angelospanag/envx)

Layered environment configuration for Go: typed parsing, secret redaction, and
errors that name the variable to fix.

```sh
go get github.com/angelospanag/envx
```

Requires Go 1.27+ (for generic methods).

## Usage

```go
type Config struct {
	Port        int                 `env:"PORT,default=8080"`
	DatabaseURL string              `env:"DATABASE_URL,required"`
	Password    envx.Secret[string] `env:"DB_PASSWORD"`
	Timeout     time.Duration       `env:"TIMEOUT,default=30s"`
}

cfg, err := envx.Load[Config]() // process environment, then .env
```

Every problem is reported at once, with the layer that supplied the bad value:

```
envx: 2 problems loading Config:
  PORT: must be an integer, got "abc" (from .env.local)
  DATABASE_URL: required but not set
```

`Secret[T]` redacts itself in every formatting, logging and marshalling path;
`Get()` returns the plaintext.

To load several structs from one snapshot, build a `Loader`:

```go
l, err := envx.New(envx.OSEnv(), envx.DotEnv(".env.local", ".env"))
api, err := l.Load[APIConfig]()
origin, _ := l.Origin("PORT") // ".env.local"
```

`envx.Must[Config]()` panics instead of returning an error.

## Sources

Earlier sources win.

| Source | Reads |
|---|---|
| `OSEnv()` | the process environment |
| `DotEnv(paths...)` | `.env` files; missing files are fine |
| `SecretsDir(dir)` | file-mounted secrets; filename is the key |
| `Values(m)`, `NamedValues(name, m)` | an in-memory map |

Implement `Source` (`Name() string`, `Values() (map[string]string, error)`)
for anything else.

`.env` files are parsed with [godotenv](https://github.com/joho/godotenv) and
never written to `os.Environ`. `${VAR}` expands only against earlier keys in
the same file. `${VAR:-default}` is not supported.

## Struct tags

| Option | Effect |
|---|---|
| `default=X` | used when no source supplied the key |
| `required` | `ErrMissing` when no source supplied the key |
| `notEmpty` | `ErrEmpty` when the value is `""` |
| `separator=;` | slice separator (default `,`) |
| `-` | skip the field |

Quote values containing commas: `default='a,b'`. Untagged fields use
`SCREAMING_SNAKE_CASE` (`DBPassword` → `DB_PASSWORD`).

Nested structs take `envPrefix:"DB_"`. A pointer group stays `nil` unless one of
its keys is set.

`KEY=` is set to the empty string, not unset, so `default` does not apply.

Supported types: `string`, `bool`, ints, uints, floats, `time.Duration`,
`[]byte`, slices and pointers of these, and any `encoding.TextUnmarshaler`.

## Validation and errors

Implement `Validate() error` for cross-field rules; it runs after all fields
parse. Failures are an `*envx.Error` whose `Fields` list each problem and which
works with `errors.Is` against `ErrMissing`, `ErrEmpty`, `ErrUnsupportedType`
and `ErrInvalidTag`.

## Development

[mise](https://mise.jdx.dev/) manages the pinned toolchain (Go 1.27.3, golangci-lint).

```bash
mise install
mise run check   # format, lint, test and audit; run before pushing
mise run test
mise run fuzz
```

Run `mise tasks` for the full list.

## License

MIT
