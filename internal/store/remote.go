package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// Remote is a Store backed by the Alror workspace API (contract section 5),
// used in connected mode. It behaves like FS: Save stamps UpdatedAt, Get accepts a unique id prefix
// and returns ErrNotFound otherwise, List is newest first, Append stamps At.
type Remote struct {
	c *api.Client

	// PageSize is how many deployments List asks for per page (default 500,
	// server maximum 1000). List follows `before` cursors until the end.
	PageSize int

	mu    sync.Mutex
	known map[string]bool // ids this store has already created on the server
}

// NewRemote returns a Store that reads and writes through c.
func NewRemote(c *api.Client) *Remote {
	return &Remote{c: c, PageSize: 500, known: map[string]bool{}}
}

// Client returns the underlying API client.
func (r *Remote) Client() *api.Client { return r.c }

func ctx() (context.Context, context.CancelFunc) {
	// The client applies per-attempt timeouts; this bounds retries as a whole.
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

// Save upserts the deployment: POST the first time this store sees an id,
// PUT afterwards, falling back to POST when the server does not know the id.
// The server owns the timestamps: d takes created_at and updated_at from the
// response (UpdatedAt is stamped locally first, in case the server omits it).
func (r *Remote) Save(d *domain.Deployment) error {
	c, cancel := ctx()
	defer cancel()
	d.UpdatedAt = time.Now().UTC()

	r.mu.Lock()
	known := r.known[d.ID]
	r.mu.Unlock()

	var (
		out *domain.Deployment
		err error
	)
	if known {
		out, err = r.c.UpdateDeployment(c, d)
		if errors.Is(err, api.ErrNotFound) {
			out, err = r.c.CreateDeployment(c, d)
		}
	} else {
		out, err = r.c.CreateDeployment(c, d)
	}
	if err != nil {
		return fmt.Errorf("save deployment %s: %w", d.ID, err)
	}
	if out != nil {
		if !out.UpdatedAt.IsZero() {
			d.UpdatedAt = out.UpdatedAt
		}
		if !out.CreatedAt.IsZero() {
			d.CreatedAt = out.CreatedAt
		}
	}
	r.mu.Lock()
	r.known[d.ID] = true
	r.mu.Unlock()
	return nil
}

// Get loads one deployment by id or unique prefix.
func (r *Remote) Get(id string) (*domain.Deployment, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := r.c.GetDeployment(c, id)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	case errors.Is(err, api.ErrConflict):
		return nil, fmt.Errorf("%w: %s matches more than one deployment", ErrNotFound, id)
	case err != nil:
		return nil, err
	}
	r.mu.Lock()
	r.known[d.ID] = true
	r.mu.Unlock()
	return d, nil
}

// List returns every deployment, newest first, paging with `before`.
func (r *Remote) List() ([]*domain.Deployment, error) {
	c, cancel := ctx()
	defer cancel()
	size := r.PageSize
	if size <= 0 || size > 1000 {
		size = 500
	}
	out := []*domain.Deployment{}
	before := ""
	for {
		page, err := r.c.ListDeployments(c, api.ListOptions{Limit: size, Before: before})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < size {
			return out, nil
		}
		next := page[len(page)-1].ID
		if next == before { // a server ignoring the cursor would loop forever
			return out, nil
		}
		before = next
	}
}

// Append adds one event to a deployment's log.
func (r *Remote) Append(id string, e domain.Event) error {
	c, cancel := ctx()
	defer cancel()
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	return r.c.AppendEvent(c, id, e)
}

// Events returns a deployment's events in order (none for an unknown id, like FS).
func (r *Remote) Events(id string) ([]domain.Event, error) {
	c, cancel := ctx()
	defer cancel()
	out, err := r.c.Events(c, id)
	if errors.Is(err, api.ErrNotFound) {
		return nil, nil
	}
	return out, err
}

// RecentRollbacks counts rollbacks per service since a time.
func (r *Remote) RecentRollbacks(since time.Time) (map[string]int, error) {
	c, cancel := ctx()
	defer cancel()
	return r.c.RecentRollbacks(c, since)
}
