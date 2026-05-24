package audit

import (
	"path/filepath"
	"testing"
)

func TestStoreAppendListClear(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "audit.json"))

	if err := store.Append(Entry{Event: "session.accepted", PeerID: "dev_b"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(Entry{Event: "input.mouse", PeerID: "dev_b"}); err != nil {
		t.Fatal(err)
	}

	entries, err := store.List(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d", len(entries))
	}
	if entries[0].Event != "input.mouse" {
		t.Fatalf("latest event = %q", entries[0].Event)
	}

	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	entries, err = store.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries should be empty after clear: %d", len(entries))
	}
}
