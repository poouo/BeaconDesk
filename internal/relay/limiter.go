package relay

import (
	"context"
	"sync"
	"time"
)

const tokenBucketBurstSeconds = 2

// BandwidthLimiter is a small token bucket used by the relay before forwarding
// large session messages such as JPEG frames. It is intentionally conservative:
// config value 0 or lower disables limiting.
type BandwidthLimiter struct {
	LimitKbps int

	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
}

func NewBandwidthLimiter(limitKbps int) *BandwidthLimiter {
	return &BandwidthLimiter{
		LimitKbps: limitKbps,
		tokens:    float64(limitKbps * 1000 / 8 * tokenBucketBurstSeconds),
		lastFill:  time.Now(),
	}
}

func (l *BandwidthLimiter) Wait(ctx context.Context, bytes int) error {
	if l == nil || l.LimitKbps <= 0 || bytes <= 0 {
		return nil
	}
	needed := float64(bytes)
	for {
		delay := l.reserveDelay(needed)
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *BandwidthLimiter) reserveDelay(needed float64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	rate := float64(l.LimitKbps * 1000 / 8)
	burst := rate * tokenBucketBurstSeconds
	l.tokens += now.Sub(l.lastFill).Seconds() * rate
	if l.tokens > burst {
		l.tokens = burst
	}
	l.lastFill = now

	if l.tokens >= needed {
		l.tokens -= needed
		return 0
	}
	missing := needed - l.tokens
	l.tokens = 0
	return time.Duration(missing / rate * float64(time.Second))
}
