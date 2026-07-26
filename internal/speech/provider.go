package speech

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	ErrBusy                = errors.New("speech session limit reached")
	ErrProviderUnavailable = errors.New("speech provider is not configured")
)

type Voice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type SessionConfig struct {
	Voice string
	Speed float64
}

type AudioConfig struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	BitDepth   int    `json:"bitDepth"`
}

type EventType string

const (
	EventAudio     EventType = "audio"
	EventCompleted EventType = "completed"
)

type Event struct {
	Type  EventType
	Audio []byte
}

type Session interface {
	AudioConfig() AudioConfig
	SendText(context.Context, string) error
	Finish(context.Context) error
	ReadEvent(context.Context) (Event, error)
	Close() error
}

type Provider interface {
	ID() string
	Configured() bool
	Voices() []Voice
	Open(context.Context, SessionConfig) (Session, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider != nil {
			registry.providers[provider.ID()] = provider
		}
	}
	return registry
}

func (r *Registry) Provider(id string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	provider, ok := r.providers[id]
	return provider, ok
}

func (r *Registry) Descriptors() []ProviderDescriptor {
	if r == nil {
		return []ProviderDescriptor{}
	}
	result := make([]ProviderDescriptor, 0, len(r.providers))
	for _, provider := range r.providers {
		result = append(result, ProviderDescriptor{
			ID: provider.ID(), Configured: provider.Configured(), Voices: provider.Voices(),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type ProviderDescriptor struct {
	ID         string  `json:"id"`
	Configured bool    `json:"configured"`
	Voices     []Voice `json:"voices"`
}

type Gate struct {
	mu          sync.Mutex
	globalLimit int
	perUser     int
	global      int
	users       map[string]int
}

func NewGate(globalLimit, perUserLimit int) *Gate {
	if globalLimit < 1 {
		globalLimit = 2
	}
	if perUserLimit < 1 {
		perUserLimit = 1
	}
	return &Gate{
		globalLimit: globalLimit,
		perUser:     perUserLimit,
		users:       make(map[string]int),
	}
}

func (g *Gate) Acquire(userID string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.global >= g.globalLimit || g.users[userID] >= g.perUser {
		return nil, ErrBusy
	}
	g.global++
	g.users[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.global--
			g.users[userID]--
			if g.users[userID] == 0 {
				delete(g.users, userID)
			}
		})
	}, nil
}

func (g *Gate) Snapshot() (global int, users int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.global, len(g.users)
}
