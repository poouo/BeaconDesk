package transport

import (
	"context"
	"time"

	"github.com/poouo/BeaconDesk/internal/protocol"
)

func StartHeartbeat(ctx context.Context, conn Conn, from string, interval time.Duration) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var seq int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				seq++
				msg := protocol.MustEnvelope(protocol.TypeHeartbeatPing, from, "", protocol.HeartbeatPayload{
					Sequence: seq,
					SentAt:   time.Now().UnixMilli(),
				})
				if err := conn.Write(ctx, msg); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()
	return errCh
}
