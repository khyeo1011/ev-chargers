// Package api is the REST layer. It depends only on the charger.Store and
// charger.Publisher interfaces, so it never imports AWS directly.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/khyeo1011/ev-chargers/internal/charger"
)

type Server struct {
	Store     charger.Store
	Publisher charger.Publisher
	Now       func() time.Time // injectable for tests
}

// Routes uses Go 1.22+ method-and-path patterns, so no router library needed.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /chargers", s.createCharger)
	mux.HandleFunc("GET /chargers/{id}", s.getCharger)
	mux.HandleFunc("GET /chargers", s.listChargers)
	return logRequests(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createRequest struct {
	Name    string  `json:"name"`
	Network string  `json:"network"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	PowerKW int     `json:"powerKw"`
}

func (s *Server) createCharger(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	c := charger.Charger{
		ID:        newID(),
		Name:      req.Name,
		Network:   req.Network,
		Lat:       req.Lat,
		Lng:       req.Lng,
		PowerKW:   req.PowerKW,
		CreatedAt: s.now(),
	}
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.Store.Put(r.Context(), c); err != nil {
		slog.Error("store put failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save charger")
		return
	}

	// Trade-off worth discussing in an interview: the write succeeded but the
	// event publish might fail, leaving the two out of sync. Production fixes
	// are the transactional outbox pattern or DynamoDB Streams -> EventBridge
	// Pipes, so the event is derived from the write itself.
	if err := s.Publisher.ChargerCreated(r.Context(), c); err != nil {
		slog.Error("publish failed; charger saved but event lost", "id", c.ID, "err", err)
	}

	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) getCharger(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, charger.ErrNotFound) {
		writeError(w, http.StatusNotFound, "charger not found")
		return
	}
	if err != nil {
		slog.Error("store get failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load charger")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type nearbyResult struct {
	charger.Charger
	DistanceKm float64 `json:"distanceKm"`
}

// listChargers returns everything, or with ?lat=&lng=[&radiusKm=] the chargers
// within range, nearest first.
//
// This loads every item and filters in Go. Fine for a demo, but it's a full
// table scan, which is exactly why the real team pairs DynamoDB with
// OpenSearch: OpenSearch has geo_distance queries built in.
func (s *Server) listChargers(w http.ResponseWriter, r *http.Request) {
	all, err := s.Store.List(r.Context())
	if err != nil {
		slog.Error("store list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list chargers")
		return
	}

	q := r.URL.Query()
	if q.Get("lat") == "" && q.Get("lng") == "" {
		writeJSON(w, http.StatusOK, all)
		return
	}

	lat, errLat := strconv.ParseFloat(q.Get("lat"), 64)
	lng, errLng := strconv.ParseFloat(q.Get("lng"), 64)
	if errLat != nil || errLng != nil {
		writeError(w, http.StatusBadRequest, "lat and lng must both be numbers")
		return
	}
	radius := 10.0
	if raw := q.Get("radiusKm"); raw != "" {
		radius, err = strconv.ParseFloat(raw, 64)
		if err != nil || radius <= 0 {
			writeError(w, http.StatusBadRequest, "radiusKm must be a positive number")
			return
		}
	}

	results := []nearbyResult{}
	for _, c := range all {
		if d := charger.DistanceKm(lat, lng, c.Lat, c.Lng); d <= radius {
			results = append(results, nearbyResult{Charger: c, DistanceKm: round2(d)})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].DistanceKm < results[j].DistanceKm })
	writeJSON(w, http.StatusOK, results)
}

// --- helpers ---

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path != "/healthz" { // keep k8s probes out of the logs
			slog.Info("request", "method", r.Method, "path", r.URL.Path,
				"status", rec.status, "ms", time.Since(start).Milliseconds())
		}
	})
}
