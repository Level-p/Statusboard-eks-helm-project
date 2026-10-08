package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory Store used by the unit tests and by "go run" without a database.
type Memory struct {
	mu         sync.Mutex
	nextID     int64
	components []Component
	incidents  []Incident
	PingErr    error // set in tests to simulate a database outage
}

func NewMemory() *Memory { return &Memory{} }

func (m *Memory) id() int64 { m.nextID++; return m.nextID }

func (m *Memory) Ping(context.Context) error { return m.PingErr }

func (m *Memory) Close() {}

func (m *Memory) ListComponents(context.Context) ([]Component, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]Component(nil), m.components...)
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

func (m *Memory) CreateComponent(_ context.Context, name, description string) (Component, error) {
	if err := validateComponent(name); err != nil {
		return Component{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := Component{ID: m.id(), Name: strings.TrimSpace(name), Description: description,
		Status: StatusOperational, Position: len(m.components) + 1, UpdatedAt: time.Now().UTC()}
	m.components = append(m.components, c)
	return c, nil
}

func (m *Memory) SetComponentStatus(_ context.Context, id int64, status string) (Component, error) {
	if !ValidComponentStatus(status) {
		return Component{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.components {
		if m.components[i].ID == id {
			m.components[i].Status = status
			m.components[i].UpdatedAt = time.Now().UTC()
			return m.components[i], nil
		}
	}
	return Component{}, ErrNotFound
}

func (m *Memory) ListIncidents(_ context.Context, includeResolved bool, limit int) ([]Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Incident
	for i := len(m.incidents) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if includeResolved || m.incidents[i].Status != IncidentResolved {
			out = append(out, m.incidents[i])
		}
	}
	return out, nil
}

func (m *Memory) GetIncident(_ context.Context, id int64) (Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.incidents {
		if in.ID == id {
			return in, nil
		}
	}
	return Incident{}, ErrNotFound
}

func (m *Memory) CreateIncident(_ context.Context, title, impact, message string) (Incident, error) {
	if err := validateIncident(title, impact, message); err != nil {
		return Incident{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	in := Incident{ID: m.id(), Title: strings.TrimSpace(title), Impact: impact, Status: IncidentInvestigating, CreatedAt: now,
		Updates: []IncidentUpdate{{ID: m.id(), Status: IncidentInvestigating, Message: message, CreatedAt: now}}}
	m.incidents = append(m.incidents, in)
	return in, nil
}

func (m *Memory) AddIncidentUpdate(_ context.Context, id int64, status, message string) (Incident, error) {
	if !ValidIncidentStatus(status) || strings.TrimSpace(message) == "" {
		return Incident{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.incidents {
		in := &m.incidents[i]
		if in.ID != id {
			continue
		}
		now := time.Now().UTC()
		// Newest update first, like the Postgres store
		in.Updates = append([]IncidentUpdate{{ID: m.id(), Status: status, Message: message, CreatedAt: now}}, in.Updates...)
		in.Status = status
		if status == IncidentResolved {
			in.ResolvedAt = &now
		} else {
			in.ResolvedAt = nil
		}
		return *in, nil
	}
	return Incident{}, ErrNotFound
}
