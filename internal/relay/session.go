package relay

import "time"

type Session struct {
	ID             string
	ControllerID   string
	ControlledID   string
	Mode           string
	InputAllowed   bool
	CreatedAt      time.Time
	LastActivityAt time.Time
}

type PendingRequest struct {
	ControllerID string
	ControlledID string
	Mode         string
	RequestedAt  time.Time
}

func (s *Session) Touch() {
	if s != nil {
		s.LastActivityAt = time.Now()
	}
}
