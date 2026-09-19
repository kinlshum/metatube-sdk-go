package engine

import (
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

type Option func(*Engine)

func WithEngineName(name string) Option {
	return func(e *Engine) {
		e.name = name
	}
}

func WithRequestTimeout(timeout time.Duration) Option {
	return func(e *Engine) {
		e.timeout = timeout
	}
}

// WithTraceService enables workflow tracing for the engine. When it is not set,
// the engine uses a disabled trace service, so behaviour and performance are
// unchanged for SDK consumers.
func WithTraceService(service *trace.Service) Option {
	return func(e *Engine) {
		if service != nil {
			e.traces = service
		}
	}
}

func WithActorProviderConfig(name string, config mt.Config) Option {
	return func(e *Engine) {
		e.actorProviderConfigs.Set(name, config)
	}
}

func WithMovieProviderConfig(name string, config mt.Config) Option {
	return func(e *Engine) {
		e.movieProviderConfigs.Set(name, config)
	}
}
