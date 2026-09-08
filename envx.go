// Package envx loads 12-factor configuration from layered sources into a
// typed struct, reporting every problem at once with the environment variable
// name attached. No dependencies.
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
// To populate several structs from one set of sources, build a [Loader] and
// reuse the snapshot:
//
//	l, err := envx.New(envx.OSEnv(), envx.DotEnv(".env"))
//	api, err := l.Load[APIConfig]()
//	db, err := l.Load[DatabaseConfig]()
//
// # Precedence
//
// Earlier sources win. [Load] reads the process environment before .env, so a
// checked-in file cannot override what the deployment set.
//
// # Set but empty
//
// PORT= supplies the empty string rather than leaving PORT unset, so a default
// does not apply and it fails to parse as an int just as "abc" would. It is the
// only recoverable choice: a field wanting a genuine empty string has no other
// way to say so, while empty-means-absent is expressible with notEmpty.
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
// process environment, then .env in the working directory.
func DefaultSources() []Source {
	return []Source{OSEnv(), DotEnv(".env")}
}

// Load populates a T from [DefaultSources]. Shorthand for [New] then
// [Loader.Load].
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
