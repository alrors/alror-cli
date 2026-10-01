// Package driver shifts traffic on a deployment target.
//
// A driver is the only component that touches the customer's infrastructure.
// Drivers are deliberately small: set a canary weight, promote, roll back.
package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/manaskumar3003/alror-cli/internal/config"
)

// Driver moves traffic between the stable version and the canary.
type Driver interface {
	Name() string
	SetWeight(ctx context.Context, svc config.Service, image string, weight int) error
	Promote(ctx context.Context, svc config.Service, image string) error
	Rollback(ctx context.Context, svc config.Service) error
}

// ErrNotImplemented is returned by targets planned for a later phase.
var ErrNotImplemented = errors.New("not implemented yet")

// For returns the driver for a service's target.
func For(svc config.Service) (Driver, error) {
	switch svc.Target {
	case "", "simulated":
		return &Simulated{}, nil
	case "kubernetes":
		return &Kubernetes{}, nil
	case "ecs":
		return &ECS{}, nil
	default:
		return nil, fmt.Errorf("unknown target %q for %s (simulated | kubernetes | ecs)", svc.Target, svc.Name)
	}
}

// Simulated records traffic changes in memory. It is the default target, so
// the whole release loop can be exercised without any infrastructure.
type Simulated struct {
	mu      sync.Mutex
	Weights []int
}

func (s *Simulated) Name() string { return "simulated" }

func (s *Simulated) SetWeight(_ context.Context, _ config.Service, _ string, weight int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Weights = append(s.Weights, weight)
	return nil
}

func (s *Simulated) Promote(ctx context.Context, svc config.Service, image string) error {
	return s.SetWeight(ctx, svc, image, 100)
}

func (s *Simulated) Rollback(ctx context.Context, svc config.Service) error {
	return s.SetWeight(ctx, svc, "", 0)
}

// Kubernetes drives an Argo Rollouts resource named after the service, using
// the kubectl argo rollouts plugin. Each SetWeight advances one paused step.
type Kubernetes struct{}

func (k *Kubernetes) Name() string { return "kubernetes" }

func (k *Kubernetes) SetWeight(ctx context.Context, svc config.Service, image string, weight int) error {
	if weight <= 5 && image != "" {
		// First step: point the rollout at the new image, which starts the canary.
		return kubectl(ctx, svc, "argo", "rollouts", "set", "image", svc.Name, svc.Name+"="+image)
	}
	return kubectl(ctx, svc, "argo", "rollouts", "promote", svc.Name)
}

func (k *Kubernetes) Promote(ctx context.Context, svc config.Service, _ string) error {
	return kubectl(ctx, svc, "argo", "rollouts", "promote", svc.Name, "--full")
}

func (k *Kubernetes) Rollback(ctx context.Context, svc config.Service) error {
	return kubectl(ctx, svc, "argo", "rollouts", "abort", svc.Name)
}

func kubectl(ctx context.Context, svc config.Service, args ...string) error {
	if svc.Cluster != "" {
		args = append(args, "--context", svc.Cluster)
	}
	if svc.Namespace != "" {
		args = append(args, "-n", svc.Namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// ECS will shift weights between two target groups behind an ALB listener.
// It is scheduled for phase 2 of the roadmap.
type ECS struct{}

func (e *ECS) Name() string { return "ecs" }

func (e *ECS) SetWeight(context.Context, config.Service, string, int) error {
	return fmt.Errorf("ecs driver: %w (planned for phase 2)", ErrNotImplemented)
}

func (e *ECS) Promote(context.Context, config.Service, string) error {
	return fmt.Errorf("ecs driver: %w (planned for phase 2)", ErrNotImplemented)
}

func (e *ECS) Rollback(context.Context, config.Service) error {
	return fmt.Errorf("ecs driver: %w (planned for phase 2)", ErrNotImplemented)
}
