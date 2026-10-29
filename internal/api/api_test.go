package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/khyeo1011/ev-chargers/internal/charger"
	"github.com/khyeo1011/ev-chargers/internal/memory"
)

func newTestServer() (*Server, *memory.Store, *memory.Publisher) {
	store := memory.NewStore()
	pub := &memory.Publisher{}
	fixed := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	return &Server{Store: store, Publisher: pub, Now: func() time.Time { return fixed }}, store, pub
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

func TestCreateCharger_SavesAndPublishes(t *testing.T) {
	srv, store, pub := newTestServer()
	h := srv.Routes()

	rec := do(t, h, "POST", "/chargers", map[string]any{
		"name": "Metrotown Supercharger", "network": "Tesla",
		"lat": 49.2276, "lng": -123.0076, "powerKw": 250,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	var got charger.Charger
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Error("expected an ID to be assigned")
	}
	if _, err := store.Get(context.Background(), got.ID); err != nil {
		t.Errorf("charger not in store: %v", err)
	}
	if len(pub.Events) != 1 || pub.Events[0].ID != got.ID {
		t.Errorf("expected one ChargerCreated event for %s, got %+v", got.ID, pub.Events)
	}
}

func TestCreateCharger_Validation(t *testing.T) {
	cases := map[string]map[string]any{
		"missing name":  {"lat": 49.2, "lng": -123.1, "powerKw": 50},
		"bad latitude":  {"name": "x", "lat": 120, "lng": -123.1, "powerKw": 50},
		"zero power":    {"name": "x", "lat": 49.2, "lng": -123.1, "powerKw": 0},
		"unknown field": {"name": "x", "lat": 49.2, "lng": -123.1, "powerKw": 50, "color": "red"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _, pub := newTestServer()
			rec := do(t, srv.Routes(), "POST", "/chargers", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if len(pub.Events) != 0 {
				t.Error("no event should be published for an invalid request")
			}
		})
	}
}

func TestGetCharger_NotFound(t *testing.T) {
	srv, _, _ := newTestServer()
	rec := do(t, srv.Routes(), "GET", "/chargers/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListChargers_NearbySortedByDistance(t *testing.T) {
	srv, store, _ := newTestServer()
	ctx := context.Background()
	_ = store.Put(ctx, charger.Charger{ID: "far", Name: "Whistler", Lat: 50.1163, Lng: -122.9574, PowerKW: 50})
	_ = store.Put(ctx, charger.Charger{ID: "mid", Name: "Burnaby", Lat: 49.2488, Lng: -122.9805, PowerKW: 150})
	_ = store.Put(ctx, charger.Charger{ID: "near", Name: "Downtown", Lat: 49.2830, Lng: -123.1200, PowerKW: 50})

	// Searching from downtown Vancouver with a 20 km radius should skip Whistler.
	rec := do(t, srv.Routes(), "GET", "/chargers?lat=49.2827&lng=-123.1207&radiusKm=20", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var got []nearbyResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "near" || got[1].ID != "mid" {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestListChargers_BadQuery(t *testing.T) {
	srv, _, _ := newTestServer()
	rec := do(t, srv.Routes(), "GET", "/chargers?lat=abc&lng=1", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// failingPublisher shows how interfaces make failure paths testable.
type failingPublisher struct{}

func (failingPublisher) ChargerCreated(context.Context, charger.Charger) error {
	return errors.New("eventbridge unavailable")
}

func TestCreateCharger_PublishFailureStillReturns201(t *testing.T) {
	store := memory.NewStore()
	srv := &Server{Store: store, Publisher: failingPublisher{}}
	rec := do(t, srv.Routes(), "POST", "/chargers", map[string]any{
		"name": "x", "lat": 49.2, "lng": -123.1, "powerKw": 50,
	})
	// Documents current behaviour: the write wins even if the event is lost.
	// See the outbox-pattern comment in api.go.
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
}
