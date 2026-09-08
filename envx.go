// Package envx loads 12-factor configuration from layered sources into a
// typed struct, reporting every problem at once with the environment variable
// name attached.
//
// It is an environment loader, not a config framework: no YAML, no CLI flags,
// no live reload. It has no dependencies.
//
// The common case is one call:
//
//	type Config struct {
//		Port     int                 `env:"PORT,default=8080"`
//		Database string              `env:"DATABASE_URL,required"`
//		Password envx.Secret[string] `env:"DB_PASSWORD"`
//		Timeout  time.Duration       `env:"TIMEOUT,default=30s"`
//	}
//
//	cfg, err := envx.Load[Config]()
//
// When several structs share one set of sources, build a Loader and reuse the
// snapshot:
//
//	l, err := envx.New(
//		envx.OSEnv(),
//		envx.DotEnv(".env.local", ".env"),
//		envx.SecretsDir("/run/secrets"),
//	)
//	api, err := l.Load[APIConfig]()
//	db, err := l.Load[DatabaseConfig]()
//
// # Precedence
//
// Earlier sources win. The default for [Load] is the process environment
// first, then .env — so a value baked into a checked-in .env file can never
// silently override what the deployment actually set.
//
// # Set but empty
//
// PORT= supplies the empty string; it does not leave PORT unset. A default
// therefore does not apply to it, and it will fail to parse as an int just as
// "abc" would. This is the only choice that stays recoverable: a field that
// genuinely wants an empty string has no other way to say so, whereas a field
// that wants empty-means-absent can say that with notEmpty and a default.
package envx

// Option configures the package-level [Load].
type Option func(*loadOptions)

type loadOptions struct {
	sources []Source
}

// WithSources replaces the default sources.
func WithSources(sources ...Source) Option {
	return func(o *loadOptions) { o.sources = sources }
}

// DefaultSources returns the layers [Load] uses when none are given: the
// process environment, then a .env file in the working directory.
func DefaultSources() []Source {
	return []Source{OSEnv(), DotEnv(".env")}
}

// Load reads the default sources and populates a T. It is shorthand for
// [New] followed by [Loader.Load].
func Load[T any](opts ...Option) (T, error) {
	o := loadOptions{sources: DefaultSources()}
	for _, fn := range opts {
		fn(&o)
	}
	l, err := New(o.sources...)
	if err != nil {
		var zero T
		return zero, err
	}
	return l.Load[T]()
}

// Must is [Load] for main, where a bad config should stop the process.
func Must[T any](opts ...Option) T {
	cfg, err := Load[T](opts...)
	if err != nil {
		panic(err)
	}
	return cfg
}
