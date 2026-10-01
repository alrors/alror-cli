// Package metrics fetches canary and baseline observations for verification.
package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/config"
)

// Track identifies which side of a rollout a series belongs to.
type Track string

const (
	Canary   Track = "canary"
	Baseline Track = "baseline"
)

// DefaultMetrics are verified when alror.yaml lists no queries.
var DefaultMetrics = []string{"error_rate", "latency_p95"}

// Provider returns observations for one metric and track over a window.
type Provider interface {
	Name() string
	Sample(ctx context.Context, service, metric string, track Track, window time.Duration) ([]float64, error)
}

// New builds the provider selected in alror.yaml.
func New(cfg *config.Config) (Provider, error) {
	switch cfg.Metrics.Provider {
	case "", "synthetic":
		return NewSynthetic(nil), nil
	case "prometheus":
		if cfg.Metrics.URL == "" {
			return nil, fmt.Errorf("metrics.url is required for prometheus")
		}
		return &Prometheus{URL: strings.TrimRight(cfg.Metrics.URL, "/"), Queries: cfg.Metrics.Queries}, nil
	case "datadog":
		return NewDatadog(cfg.Metrics.URL, cfg.Metrics.Queries)
	default:
		return nil, fmt.Errorf("unknown metrics provider %q (synthetic | prometheus | datadog)", cfg.Metrics.Provider)
	}
}

// Names returns the metric names to verify for a configuration.
func Names(cfg *config.Config) []string {
	if len(cfg.Metrics.Queries) == 0 || cfg.Metrics.Provider == "synthetic" {
		return DefaultMetrics
	}
	out := make([]string, 0, len(cfg.Metrics.Queries))
	for k := range cfg.Metrics.Queries {
		out = append(out, k)
	}
	return out
}

func render(tmpl, service string, track Track) string {
	return strings.NewReplacer("{{service}}", service, "{{track}}", string(track)).Replace(tmpl)
}
