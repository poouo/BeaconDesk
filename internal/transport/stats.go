package transport

import (
	"sync/atomic"
	"time"
)

type Stats struct {
	connectedAt    time.Time
	lastMessageAt  atomic.Int64
	framesSent     atomic.Int64
	framesReceived atomic.Int64
}

func NewStats() *Stats {
	now := time.Now()
	s := &Stats{connectedAt: now}
	s.Touch()
	return s
}

func (s *Stats) Touch() {
	s.lastMessageAt.Store(time.Now().UnixMilli())
}

func (s *Stats) LastMessageAt() time.Time {
	return time.UnixMilli(s.lastMessageAt.Load())
}

func (s *Stats) ConnectedAt() time.Time {
	return s.connectedAt
}

func (s *Stats) IncFramesSent() {
	s.framesSent.Add(1)
}

func (s *Stats) IncFramesReceived() {
	s.framesReceived.Add(1)
}

func (s *Stats) FramesSent() int64 {
	return s.framesSent.Load()
}

func (s *Stats) FramesReceived() int64 {
	return s.framesReceived.Load()
}
