// Package store persists deployments and their events on the local file system.
//
// Layout under the state directory (default .alror/ next to alror.yaml):
//
//	.alror/
//	  deployments/<id>.json   current state of each deployment (atomic rewrite)
//	  events/<id>.jsonl       append-only event log, one JSON object per line
//
// Plain files keep the MVP dependency-free and easy to inspect, back up or
// commit as CI artifacts. In connected mode, Remote implements the same Store
// interface over the Alror workspace API (Postgres behind it).
package store

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// Store is the persistence contract used by the rollout engine and the CLI.
type Store interface {
	Save(d *domain.Deployment) error
	Get(id string) (*domain.Deployment, error)
	List() ([]*domain.Deployment, error)
	Append(id string, e domain.Event) error
	Events(id string) ([]domain.Event, error)
	RecentRollbacks(since time.Time) (map[string]int, error)
}

// ErrNotFound is returned when a deployment id does not exist.
var ErrNotFound = errors.New("deployment not found")

// FS is a Store backed by the file system.
type FS struct {
	dir string
	mu  sync.Mutex
}

// Open creates the directory layout if needed and returns a store.
func Open(dir string) (*FS, error) {
	for _, sub := range []string{"deployments", "events"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &FS{dir: dir}, nil
}

// Dir returns the root state directory.
func (s *FS) Dir() string { return s.dir }

// NewID returns a sortable, collision-resistant deployment id: dep_<time>_<rand>.
func NewID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("dep_%s_%s", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(b))
}

// Save writes the deployment atomically (temp file + rename).
func (s *FS) Save(d *domain.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	path := s.depPath(d.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get loads one deployment. A unique prefix of the id is accepted.
func (s *FS) Get(id string) (*domain.Deployment, error) {
	path := s.depPath(id)
	if _, err := os.Stat(path); err != nil {
		matches, _ := filepath.Glob(filepath.Join(s.dir, "deployments", id+"*.json"))
		if len(matches) != 1 {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		path = matches[0]
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d domain.Deployment
	return &d, json.Unmarshal(raw, &d)
}

// List returns all deployments, newest first.
func (s *FS) List() ([]*domain.Deployment, error) {
	files, err := filepath.Glob(filepath.Join(s.dir, "deployments", "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Deployment, 0, len(files))
	for _, f := range files {
		d, err := s.Get(strings.TrimSuffix(filepath.Base(f), ".json"))
		if err == nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Append adds one event to a deployment's log.
func (s *FS) Append(id string, e domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "events", id+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// Events returns a deployment's events in order.
func (s *FS) Events(id string) ([]domain.Event, error) {
	f, err := os.Open(filepath.Join(s.dir, "events", id+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []domain.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e domain.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// RecentRollbacks counts rollbacks per service since a time, for risk scoring.
func (s *FS) RecentRollbacks(since time.Time) (map[string]int, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, d := range all {
		if d.Status == domain.StatusRolledBack && d.UpdatedAt.After(since) {
			out[d.Service]++
		}
	}
	return out, nil
}

func (s *FS) depPath(id string) string { return filepath.Join(s.dir, "deployments", id+".json") }
