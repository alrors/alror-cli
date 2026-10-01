package store_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/api"
	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/internal/domain"
	"github.com/manaskumar3003/alror-cli/internal/store"
)

// The conformance suite runs the same behaviour checks against every Store
// implementation, so the engine and CLI can rely on identical semantics.

func TestConformanceFS(t *testing.T) {
	conformance(t, func(t *testing.T) store.Store {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return st
	})
}

func TestConformanceRemote(t *testing.T) {
	conformance(t, func(t *testing.T) store.Store {
		f, srv := apitest.Start()
		t.Cleanup(srv.Close)
		cfg := f.Config()
		cfg.Services = append(cfg.Services, cfg.Services[0], cfg.Services[0])
		cfg.Services[len(cfg.Services)-2].Name = "a"
		cfg.Services[len(cfg.Services)-1].Name = "b"
		f.SetConfig(cfg)
		return store.NewRemote(api.New(srv.URL, apitest.DefaultKey, api.WithRetries(1, time.Millisecond)))
	})
}

func conformance(t *testing.T, open func(t *testing.T) store.Store) {
	t.Run("SaveGetListEvents", func(t *testing.T) {
		st := open(t)
		old := &domain.Deployment{ID: "dep_1_aaa", Service: "a", Status: domain.StatusRolledBack, CreatedAt: time.Now().Add(-time.Hour).UTC()}
		recent := &domain.Deployment{ID: "dep_2_bbb", Service: "b", Status: domain.StatusPromoted, CreatedAt: time.Now().UTC()}
		for _, d := range []*domain.Deployment{old, recent} {
			if err := st.Save(d); err != nil {
				t.Fatal(err)
			}
			if d.UpdatedAt.IsZero() {
				t.Fatal("Save must stamp UpdatedAt")
			}
		}

		got, err := st.Get("dep_2") // unique prefix
		if err != nil || got.Service != "b" || got.ID != "dep_2_bbb" {
			t.Fatalf("prefix get: %v %+v", err, got)
		}
		if _, err := st.Get("dep_"); err == nil {
			t.Fatal("ambiguous prefix must not resolve")
		}
		if _, err := st.Get("dep_9"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing id: want ErrNotFound, got %v", err)
		}

		all, err := st.List()
		if err != nil || len(all) != 2 || all[0].ID != "dep_2_bbb" {
			t.Fatalf("list should be newest first: %v %+v", err, all)
		}

		_ = st.Append(old.ID, domain.Event{Kind: domain.EventCreated, Message: "one"})
		_ = st.Append(old.ID, domain.Event{Kind: domain.EventRolledBack, Message: "two", Weight: 5})
		events, err := st.Events(old.ID)
		if err != nil || len(events) != 2 || events[1].Message != "two" || events[1].Weight != 5 {
			t.Fatalf("events: %v %+v", err, events)
		}
		if events[0].At.IsZero() {
			t.Fatal("Append must stamp At")
		}

		rb, err := st.RecentRollbacks(time.Now().Add(-24 * time.Hour))
		if err != nil || rb["a"] != 1 || rb["b"] != 0 {
			t.Fatalf("recent rollbacks: %v %v", err, rb)
		}
		rb, _ = st.RecentRollbacks(time.Now().Add(time.Hour))
		if rb["a"] != 0 {
			t.Fatalf("rollbacks before since must not count: %v", rb)
		}
	})

	t.Run("SaveUpdatesInPlace", func(t *testing.T) {
		st := open(t)
		d := &domain.Deployment{ID: "dep_3_ccc", Service: "a", Image: "img:1", Status: domain.StatusPending, CreatedAt: time.Now().UTC(),
			Risk: domain.RiskReport{Score: 42, Level: domain.LevelMedium, Factors: []domain.Factor{{Name: "x", Detail: "y", Points: 42}}},
			Plan: domain.Plan{Strategy: "canary", Steps: []domain.Step{{Weight: 5, Bake: 10 * time.Minute}, {Weight: 100}}}}
		if err := st.Save(d); err != nil {
			t.Fatal(err)
		}
		d.Status, d.StepIndex, d.Weight, d.Reason = domain.StatusRolling, 1, 25, "why"
		if err := st.Save(d); err != nil {
			t.Fatal(err)
		}
		got, err := st.Get(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != domain.StatusRolling || got.Weight != 25 || got.StepIndex != 1 || got.Reason != "why" {
			t.Fatalf("update not persisted: %+v", got)
		}
		if got.Plan.Steps[0].Bake != 10*time.Minute || got.Risk.Factors[0].Points != 42 {
			t.Fatalf("nested fields lost: %+v", got)
		}
		if all, _ := st.List(); len(all) != 1 {
			t.Fatalf("second save must not duplicate: %d", len(all))
		}
	})

	t.Run("EmptyAndUnknown", func(t *testing.T) {
		st := open(t)
		all, err := st.List()
		if err != nil || len(all) != 0 {
			t.Fatalf("empty list: %v %v", err, all)
		}
		ev, err := st.Events("dep_nope")
		if err != nil || len(ev) != 0 {
			t.Fatalf("events of unknown id: %v %v", err, ev)
		}
		rb, err := st.RecentRollbacks(time.Now().Add(-time.Hour))
		if err != nil || len(rb) != 0 {
			t.Fatalf("rollbacks on empty store: %v %v", err, rb)
		}
	})
}

func TestRemoteSaveFallsBackToCreate(t *testing.T) {
	f, srv := apitest.Start()
	defer srv.Close()
	st := store.NewRemote(api.New(srv.URL, apitest.DefaultKey))
	d := &domain.Deployment{ID: "dep_4_ddd", Service: "checkout-api", CreatedAt: time.Now()}
	if err := st.Save(d); err != nil { // POST, the id is now known
		t.Fatal(err)
	}
	f.DeleteDeployment(d.ID) // the server lost it; the next PUT 404s
	d.Status = domain.StatusPromoted
	if err := st.Save(d); err != nil {
		t.Fatal(err)
	}
	if got := f.Deployment(d.ID); got == nil || got.Status != domain.StatusPromoted {
		t.Fatalf("deployment not recreated: %+v", got)
	}
	var posts, puts int
	for _, r := range f.Requests() {
		switch {
		case r.Method == "POST" && r.Path == "/api/v1/deployments":
			posts++
		case r.Method == "PUT":
			puts++
		}
	}
	if posts != 2 || puts != 1 {
		t.Fatalf("posts=%d puts=%d, want 2 and 1", posts, puts)
	}
}

func TestRemoteSaveUnknownServiceIsValidationError(t *testing.T) {
	_, srv := apitest.Start()
	defer srv.Close()
	st := store.NewRemote(api.New(srv.URL, apitest.DefaultKey))
	err := st.Save(&domain.Deployment{ID: "dep_5", Service: "ghost"})
	if _, ok := api.IsValidation(err); !ok {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestRemoteListPagesThroughEverything(t *testing.T) {
	f, srv := apitest.Start()
	defer srv.Close()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 1203 {
		// Pairs share a created_at, so paging must also order by id.
		f.PutDeployment(&domain.Deployment{ID: fmt.Sprintf("dep_%05d", i), Service: "checkout-api", CreatedAt: base.Add(time.Duration(i/2) * time.Minute)})
	}
	st := store.NewRemote(api.New(srv.URL, apitest.DefaultKey))
	all, err := st.List()
	if err != nil || len(all) != 1203 {
		t.Fatalf("list = %d, %v", len(all), err)
	}
	seen := map[string]bool{}
	for i, d := range all {
		if seen[d.ID] {
			t.Fatalf("duplicate %s", d.ID)
		}
		seen[d.ID] = true
		if i > 0 && all[i-1].ID < d.ID {
			t.Fatalf("not newest first at %d: %s then %s", i, all[i-1].ID, d.ID)
		}
	}
	pages := 0
	for _, r := range f.Requests() {
		if r.Method == "GET" && r.Path == "/api/v1/deployments" {
			pages++
			if !strings.Contains(r.Query, "limit=500") {
				t.Fatalf("query = %s", r.Query)
			}
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3", pages)
	}
}

func TestRemoteSaveTakesServerTimestamps(t *testing.T) {
	f, srv := apitest.Start()
	defer srv.Close()
	st := store.NewRemote(api.New(srv.URL, apitest.DefaultKey))
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := &domain.Deployment{ID: "dep_ts", Service: "checkout-api", CreatedAt: created}
	f.Advance(2 * time.Hour) // the server's clock is ahead of ours
	if err := st.Save(d); err != nil {
		t.Fatal(err)
	}
	if time.Until(d.UpdatedAt) < 100*time.Minute || !d.CreatedAt.Equal(created) {
		t.Fatalf("updated_at should come from the server: %+v", d)
	}
	d.CreatedAt = time.Now() // the server keeps the original created_at
	if err := st.Save(d); err != nil {
		t.Fatal(err)
	}
	if !d.CreatedAt.Equal(created) {
		t.Fatalf("created_at = %v, want %v", d.CreatedAt, created)
	}
}
