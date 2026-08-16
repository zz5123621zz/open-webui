package dictation

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrBusy                = errors.New("dictation session limit reached")
	ErrProviderUnavailable = errors.New("dictation provider is not configured")
	ErrProviderAuth        = errors.New("dictation provider authentication failed")
	ErrProviderNotGranted  = errors.New("dictation provider resource is not granted")
	ErrProviderBusy        = errors.New("dictation provider is busy")
	ErrNoSpeech            = errors.New("dictation contained no recognizable speech")
)

type SessionConfig struct {
	UserID  string
	Context []string
}

type EventType string

const (
	EventTranscript EventType = "transcript"
	EventCompleted  EventType = "completed"
)

type Event struct {
	Type     EventType
	Text     string
	Definite bool
}

type Session interface {
	SendAudio(context.Context, []byte) error
	Finish(context.Context, []byte) error
	ReadEvent(context.Context) (Event, error)
	Close() error
}

type Provider interface {
	ID() string
	Configured() bool
	Open(context.Context, SessionConfig) (Session, error)
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
