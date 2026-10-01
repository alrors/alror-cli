// Package config loads alror.yaml, the per-repository configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileName is the configuration file Alror looks for, walking up from the working directory.
const FileName = "alror.yaml"

// Config is the root of alror.yaml.
type Config struct {
	Project  string    `yaml:"project" json:"project"`
	Server   string    `yaml:"server,omitempty" json:"-"` // Alror workspace URL; empty = local mode
	Services []Service `yaml:"services" json:"services"`
	Metrics  Metrics   `yaml:"metrics" json:"metrics"`
	Policy   Policy    `yaml:"policy" json:"policy"`
	Notify   Notify    `yaml:"notify" json:"notify"`

	// Dir is the directory that holds alror.yaml (not serialised).
	Dir string `yaml:"-" json:"-"`
}

// Service maps source paths to a deployable unit and its target.
type Service struct {
	Name      string   `yaml:"name" json:"name"`
	Paths     []string `yaml:"paths" json:"paths"`                   // glob-ish prefixes, e.g. services/checkout/
	Target    string   `yaml:"target" json:"target"`                 // simulated | kubernetes | ecs
	Cluster   string   `yaml:"cluster,omitempty" json:"cluster"`     // kube context or ECS cluster
	Namespace string   `yaml:"namespace,omitempty" json:"namespace"` // kube namespace
	Critical  bool     `yaml:"critical,omitempty" json:"critical"`   // raises risk when touched
}

// Metrics selects and configures the metrics provider used for verification.
type Metrics struct {
	Provider string `yaml:"provider" json:"provider"` // synthetic | prometheus | datadog
	URL      string `yaml:"url,omitempty" json:"url,omitempty"`
	// Queries per metric name. {{service}} and {{track}} (canary|baseline) are substituted.
	Queries map[string]string `yaml:"queries,omitempty" json:"queries,omitempty"`
}

// Policy holds thresholds for verification and auto-rollback.
type Policy struct {
	// MaxRegression is the relative increase (0.25 = +25%) tolerated per metric.
	MaxRegression map[string]float64 `yaml:"max_regression" json:"max_regression"`
	// Alpha is the significance level for the statistical test.
	Alpha float64 `yaml:"alpha" json:"alpha"`
	// AutoRollback enables rollback without a human. False = shadow mode (recommend only).
	AutoRollback bool `yaml:"auto_rollback" json:"auto_rollback"`
	// BakeScale multiplies every bake time; useful to speed up demos (e.g. 0.01).
	BakeScale float64 `yaml:"bake_scale,omitempty" json:"bake_scale,omitempty"`
}

// Notify configures outbound notifications.
type Notify struct {
	SlackWebhook string `yaml:"slack_webhook,omitempty" json:"slack_webhook,omitempty"`
}

// Default returns a configuration suitable for `alror init`.
func Default(project string) *Config {
	return &Config{
		Project: project,
		Services: []Service{
			{Name: "checkout-api", Paths: []string{"services/checkout/"}, Target: "simulated", Critical: true},
			{Name: "web-frontend", Paths: []string{"web/"}, Target: "simulated"},
		},
		Metrics: Metrics{Provider: "synthetic"},
		Policy: Policy{
			MaxRegression: map[string]float64{"error_rate": 0.25, "latency_p95": 0.15},
			Alpha:         0.05,
			AutoRollback:  true,
		},
	}
}

// Load finds alror.yaml from dir upwards, parses and validates it.
func Load(dir string) (*Config, error) {
	c, err := Read(dir)
	if err != nil {
		return nil, err
	}
	return c, c.Validate()
}

// Read finds alror.yaml from dir upwards and parses it with defaults applied,
// but does not validate it. In connected mode alror.yaml may hold only
// `server:` (and optionally service paths), so it is not required to be complete.
func Read(dir string) (*Config, error) {
	path, err := Find(dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Dir = filepath.Dir(path)
	c.ApplyDefaults()
	return &c, nil
}

// Path returns the location of alror.yaml for this configuration.
func (c *Config) Path() string { return filepath.Join(c.Dir, FileName) }

// Find walks up from dir until it finds alror.yaml.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, FileName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

// ErrNotFound means no alror.yaml exists in this directory or any parent.
var ErrNotFound = errors.New("no alror.yaml found (run `alror init`)")

// Save writes the config as YAML to path.
func (c *Config) Save(path string) error {
	raw, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := "# Alror configuration. Docs: run `alror docs`.\n"
	return os.WriteFile(path, append([]byte(header), raw...), 0o644)
}

// ApplyDefaults fills in the documented defaults for unset fields.
func (c *Config) ApplyDefaults() {
	if c.Metrics.Provider == "" {
		c.Metrics.Provider = "synthetic"
	}
	if c.Policy.Alpha == 0 {
		c.Policy.Alpha = 0.05
	}
	if c.Policy.MaxRegression == nil {
		c.Policy.MaxRegression = map[string]float64{"error_rate": 0.25, "latency_p95": 0.15}
	}
	if c.Policy.BakeScale == 0 {
		c.Policy.BakeScale = 1
	}
	for i := range c.Services {
		if c.Services[i].Target == "" {
			c.Services[i].Target = "simulated"
		}
	}
}

// Validate checks the configuration for obvious mistakes.
func (c *Config) Validate() error {
	if len(c.Services) == 0 {
		return errors.New("alror.yaml: at least one service is required")
	}
	seen := map[string]bool{}
	for _, s := range c.Services {
		if s.Name == "" {
			return errors.New("alror.yaml: every service needs a name")
		}
		if seen[s.Name] {
			return fmt.Errorf("alror.yaml: duplicate service %q", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}

// Service returns the service with the given name.
func (c *Config) Service(name string) (Service, bool) {
	for _, s := range c.Services {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}

// ServicesForPath returns the services whose path prefixes match a changed file.
func (c *Config) ServicesForPath(file string) []Service {
	file = filepath.ToSlash(file)
	var out []Service
	for _, s := range c.Services {
		for _, p := range s.Paths {
			if strings.HasPrefix(file, filepath.ToSlash(p)) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// Bake scales a bake duration by the configured BakeScale.
func (c *Config) Bake(d time.Duration) time.Duration {
	return time.Duration(float64(d) * c.Policy.BakeScale)
}

// StateDir is where the file-system store keeps deployments and events.
func (c *Config) StateDir() string {
	return filepath.Join(c.Dir, ".alror")
}
