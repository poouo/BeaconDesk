package desktop

import (
	"encoding/binary"
	"hash/fnv"
	"time"
)

type ChangeDetector struct {
	StaticFrameInterval time.Duration
	initialized         bool
	lastHash            uint64
	lastSentAt          time.Time
}

type ChangeDecision struct {
	Send    bool
	Changed bool
}

func (d *ChangeDetector) Observe(frame Frame) ChangeDecision {
	if d.StaticFrameInterval <= 0 {
		d.StaticFrameInterval = 5 * time.Second
	}

	now := frameTime(frame)
	hash := hashFrame(frame)
	changed := !d.initialized || hash != d.lastHash
	if changed {
		d.initialized = true
		d.lastHash = hash
		d.lastSentAt = now
		return ChangeDecision{Send: true, Changed: true}
	}
	if d.lastSentAt.IsZero() || now.Sub(d.lastSentAt) >= d.StaticFrameInterval {
		d.lastSentAt = now
		return ChangeDecision{Send: true, Changed: false}
	}
	return ChangeDecision{Send: false, Changed: false}
}

func frameTime(frame Frame) time.Time {
	if frame.Timestamp > 0 {
		return time.UnixMilli(frame.Timestamp)
	}
	return time.Now()
}

func hashFrame(frame Frame) uint64 {
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(frame.Width))
	_, _ = h.Write(b[:])
	binary.LittleEndian.PutUint64(b[:], uint64(frame.Height))
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(frame.Codec))
	_, _ = h.Write(frame.Data)
	return h.Sum64()
}
