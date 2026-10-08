// Package store holds the StatusBoard data model and the storage interface.
// Two implementations exist: Postgres (production) and Memory (tests and local demos).
package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Component statuses, from best to worst.
const (
	StatusOperational   = "operational"
	StatusDegraded      = "degraded"
	StatusPartialOutage = "partial_outage"
	StatusMajorOutage   = "major_outage"
)

// Incident lifecycle states.
const (
	IncidentInvestigating = "investigating"
	IncidentIdentified    = "identified"
	IncidentMonitoring    = "monitoring"
	IncidentResolved      = "resolved"
)

// Incident impact levels.
const (
	ImpactMinor    = "minor"
	ImpactMajor    = "major"
	ImpactCritical = "critical"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
)

var statusRank = map[string]int{
	StatusOperational:   0,
	StatusDegraded:      1,
	StatusPartialOutage: 2,
	StatusMajorOutage:   3,
}

var incidentStates = map[string]bool{
	IncidentInvestigating: true, IncidentIdentified: true, IncidentMonitoring: true, IncidentResolved: true,
}

var impacts = map[string]bool{ImpactMinor: true, ImpactMajor: true, ImpactCritical: true}

// ValidComponentStatus reports whether s is a known component status.
func ValidComponentStatus(s string) bool { _, ok := statusRank[s]; return ok }

// ValidIncidentStatus reports whether s is a known incident state.
func ValidIncidentStatus(s string) bool { return incidentStates[s] }

// ValidImpact reports whether s is a known impact level.
func ValidImpact(s string) bool { return impacts[s] }

// Worst returns the more severe of two component statuses.
func Worst(a, b string) string {
	if statusRank[b] > statusRank[a] {
		return b
	}
	return a
}

type Component struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Position    int       `json:"position"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type IncidentUpdate struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Incident struct {
	ID         int64            `json:"id"`
	Title      string           `json:"title"`
	Impact     string           `json:"impact"`
	Status     string           `json:"status"`
	CreatedAt  time.Time        `json:"created_at"`
	ResolvedAt *time.Time       `json:"resolved_at,omitempty"`
	Updates    []IncidentUpdate `json:"updates"`
}

// Store is everything the web layer needs from storage.
type Store interface {
	Ping(ctx context.Context) error
	ListComponents(ctx context.Context) ([]Component, error)
	CreateComponent(ctx context.Context, name, description string) (Component, error)
	SetComponentStatus(ctx context.Context, id int64, status string) (Component, error)
	// ListIncidents returns incidents newest first. Resolved ones are included only when includeResolved is true.
	ListIncidents(ctx context.Context, includeResolved bool, limit int) ([]Incident, error)
	GetIncident(ctx context.Context, id int64) (Incident, error)
	CreateIncident(ctx context.Context, title, impact, message string) (Incident, error)
	AddIncidentUpdate(ctx context.Context, id int64, status, message string) (Incident, error)
	Close()
}

// Seed creates the given components when the database has none yet.
func Seed(ctx context.Context, s Store, names []string) error {
	existing, err := s.ListComponents(ctx)
	if err != nil || len(existing) > 0 {
		return err
	}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			if _, err := s.CreateComponent(ctx, n, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateComponent(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > 80 {
		return ErrInvalid
	}
	return nil
}

func validateIncident(title, impact, message string) error {
	if strings.TrimSpace(title) == "" || len(title) > 200 || !ValidImpact(impact) || strings.TrimSpace(message) == "" {
		return ErrInvalid
	}
	return nil
}
