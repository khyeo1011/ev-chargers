// Package charger holds the core domain model and the interfaces the rest of
// the app depends on. Nothing in here knows about AWS, which is what makes the
// HTTP handlers easy to unit test with in-memory fakes.
package charger

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
)

// Charger is a single EV charging station.
type Charger struct {
	ID        string    `json:"id" dynamodbav:"id"`
	Name      string    `json:"name" dynamodbav:"name"`
	Network   string    `json:"network" dynamodbav:"network"`
	Lat       float64   `json:"lat" dynamodbav:"lat"`
	Lng       float64   `json:"lng" dynamodbav:"lng"`
	PowerKW   int       `json:"powerKw" dynamodbav:"powerKw"`
	CreatedAt time.Time `json:"createdAt" dynamodbav:"createdAt"`
}

// ErrNotFound is returned by a Store when an ID doesn't exist.
var ErrNotFound = errors.New("charger not found")

// Validate checks the fields a client is allowed to send.
func (c Charger) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Name) == "" {
		problems = append(problems, "name is required")
	}
	if c.Lat < -90 || c.Lat > 90 {
		problems = append(problems, "lat must be between -90 and 90")
	}
	if c.Lng < -180 || c.Lng > 180 {
		problems = append(problems, "lng must be between -180 and 180")
	}
	if c.PowerKW <= 0 {
		problems = append(problems, "powerKw must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Store persists chargers. The real implementation is DynamoDB; tests and
// local runs use the in-memory one.
type Store interface {
	Put(ctx context.Context, c Charger) error
	Get(ctx context.Context, id string) (Charger, error)
	List(ctx context.Context) ([]Charger, error)
}

// Publisher announces domain events. The real implementation is EventBridge.
type Publisher interface {
	ChargerCreated(ctx context.Context, c Charger) error
}

// DistanceKm is the great-circle (haversine) distance between two points.
func DistanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(a))
}
