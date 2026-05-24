package desktop

import (
	"testing"
	"time"
)

func TestChangeDetector(t *testing.T) {
	d := ChangeDetector{StaticFrameInterval: 5 * time.Second}
	base := time.Now()
	frame := Frame{
		Width:     2,
		Height:    2,
		Codec:     "jpeg",
		Timestamp: base.UnixMilli(),
		Data:      []byte("same"),
	}

	first := d.Observe(frame)
	if !first.Send || !first.Changed {
		t.Fatalf("first frame = %+v", first)
	}

	frame.Timestamp = base.Add(time.Second).UnixMilli()
	second := d.Observe(frame)
	if second.Send || second.Changed {
		t.Fatalf("unchanged frame too soon = %+v", second)
	}

	frame.Timestamp = base.Add(6 * time.Second).UnixMilli()
	keepalive := d.Observe(frame)
	if !keepalive.Send || keepalive.Changed {
		t.Fatalf("static keepalive = %+v", keepalive)
	}

	frame.Timestamp = base.Add(7 * time.Second).UnixMilli()
	frame.Data = []byte("changed")
	changed := d.Observe(frame)
	if !changed.Send || !changed.Changed {
		t.Fatalf("changed frame = %+v", changed)
	}
}
