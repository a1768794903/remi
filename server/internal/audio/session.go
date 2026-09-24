package audio

import (
	"sync"
	"time"
)

type Session struct {
	ID              string
	UserID          string
	DeviceID        string
	Codec           string
	SampleRate      int
	StartedAt       time.Time
	LastPacketAt    time.Time
	ReceivedBytes   int64
	ReceivedPackets int64
}

type Tracker struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewTracker() *Tracker {
	return &Tracker{sessions: make(map[string]*Session)}
}

func (t *Tracker) Start(id, userID, deviceID, codec string, sampleRate int, now time.Time) Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	session := &Session{ID: id, UserID: userID, DeviceID: deviceID, Codec: codec, SampleRate: sampleRate, StartedAt: now, LastPacketAt: now}
	t.sessions[id] = session
	return *session
}

func (t *Tracker) AddPacket(id string, bytes int, now time.Time) (Session, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[id]
	if !ok {
		return Session{}, false
	}
	session.ReceivedBytes += int64(bytes)
	session.ReceivedPackets++
	session.LastPacketAt = now
	return *session, true
}

func (t *Tracker) Finish(id string) (Session, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[id]
	if !ok {
		return Session{}, false
	}
	delete(t.sessions, id)
	return *session, true
}
