package halide

// Backend selects how a pipeline library is executed.
type Backend int

const (
	// Native loads a platform shared library and calls it through purego.
	Native Backend = iota
	// WASM runs a Halide wasm-simd module with wazero.
	WASM
)

func (b Backend) String() string {
	switch b {
	case Native:
		return "native"
	case WASM:
		return "wasm"
	default:
		return "unknown"
	}
}

type config struct {
	backend Backend
}

func defaultConfig() config {
	if defaultWASM {
		return config{backend: WASM}
	}
	return config{backend: Native}
}

// Option configures Load and OpenEmbedded.
type Option func(*config)

// WithBackend forces a backend. It overrides the halidewasm build tag.
func WithBackend(b Backend) Option {
	return func(c *config) { c.backend = b }
}

func applyOptions(opts []Option) config {
	c := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	return c
}
