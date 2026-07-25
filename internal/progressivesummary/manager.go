package progressivesummary

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateUnknown  State = "unknown"
	StateProbing  State = "probing"
	StateActive   State = "active"
	StateCooldown State = "cooldown"
	StateDisabled State = "disabled"
)

type Decision struct {
	Endpoint   string
	Model      string
	Requested  bool
	Probe      bool
	generation uint64
}

type ModelStatus struct {
	Model         string `json:"model"`
	State         State  `json:"state"`
	LastCheckedAt int64  `json:"lastCheckedAt,omitempty"`
	CooldownUntil int64  `json:"cooldownUntil,omitempty"`
}

type cacheKey struct {
	endpoint string
	model    string
}

type entry struct {
	state         State
	generation    uint64
	lastCheckedAt time.Time
	cooldownUntil time.Time
}

type Manager struct {
	mu       sync.Mutex
	entries  map[cacheKey]entry
	cooldown time.Duration
	now      func() time.Time
	next     uint64
}

func New(cooldown time.Duration) *Manager {
	if cooldown <= 0 {
		cooldown = 30 * time.Minute
	}
	return &Manager{
		entries:  make(map[cacheKey]entry),
		cooldown: cooldown,
		now:      time.Now,
	}
}

func (m *Manager) Decide(endpoint, model string) Decision {
	key := normalizedKey(endpoint, model)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	current, exists := m.entries[key]
	if exists {
		switch current.state {
		case StateActive:
			return Decision{
				Endpoint: key.endpoint, Model: key.model, Requested: true,
				generation: current.generation,
			}
		case StateProbing:
			return Decision{Endpoint: key.endpoint, Model: key.model}
		case StateCooldown:
			if now.Before(current.cooldownUntil) {
				return Decision{Endpoint: key.endpoint, Model: key.model}
			}
		}
	}
	m.next++
	current.state = StateProbing
	current.generation = m.next
	current.cooldownUntil = time.Time{}
	m.entries[key] = current
	return Decision{
		Endpoint: key.endpoint, Model: key.model, Requested: true, Probe: true,
		generation: current.generation,
	}
}

func (m *Manager) MarkAccepted(decision Decision) {
	if !decision.Requested {
		return
	}
	key := normalizedKey(decision.Endpoint, decision.Model)
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.entries[key]
	if !exists || current.generation != decision.generation {
		return
	}
	if decision.Probe && current.state != StateProbing {
		return
	}
	if !decision.Probe && current.state != StateActive {
		return
	}
	current.state = StateActive
	current.generation = decision.generation
	current.lastCheckedAt = m.now()
	current.cooldownUntil = time.Time{}
	m.entries[key] = current
}

func (m *Manager) MarkUnsupported(decision Decision) {
	if !decision.Requested {
		return
	}
	key := normalizedKey(decision.Endpoint, decision.Model)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	current, exists := m.entries[key]
	if !exists || current.generation != decision.generation {
		return
	}
	if decision.Probe && current.state != StateProbing {
		return
	}
	if !decision.Probe && current.state != StateActive {
		return
	}
	current.state = StateCooldown
	current.generation = decision.generation
	current.lastCheckedAt = now
	current.cooldownUntil = now.Add(m.cooldown)
	m.entries[key] = current
}

func (m *Manager) MarkInconclusive(decision Decision) {
	if !decision.Probe {
		return
	}
	key := normalizedKey(decision.Endpoint, decision.Model)
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.entries[key]
	if !exists || current.state != StateProbing ||
		current.generation != decision.generation {
		return
	}
	current.state = StateUnknown
	current.cooldownUntil = time.Time{}
	m.entries[key] = current
}

func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[cacheKey]entry)
}

func (m *Manager) Snapshot(endpoint string) []ModelStatus {
	endpoint = strings.TrimSpace(endpoint)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	result := make([]ModelStatus, 0, len(m.entries))
	for key, current := range m.entries {
		if key.endpoint != endpoint {
			continue
		}
		state := current.state
		if state == StateCooldown && !now.Before(current.cooldownUntil) {
			state = StateUnknown
		}
		status := ModelStatus{Model: key.model, State: state}
		if !current.lastCheckedAt.IsZero() {
			status.LastCheckedAt = current.lastCheckedAt.UnixMilli()
		}
		if state == StateCooldown {
			status.CooldownUntil = current.cooldownUntil.UnixMilli()
		}
		result = append(result, status)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Model < result[right].Model
	})
	return result
}

func normalizedKey(endpoint, model string) cacheKey {
	return cacheKey{
		endpoint: strings.TrimSpace(endpoint),
		model:    strings.TrimSpace(model),
	}
}
