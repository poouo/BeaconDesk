package trust

import (
	"path/filepath"
	"testing"
)

func TestStoreRememberListRevoke(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "trusted.json"))
	if store.IsTrusted("dev_a") {
		t.Fatal("device should not be trusted yet")
	}
	if err := store.Remember("dev_a", "view-control"); err != nil {
		t.Fatal(err)
	}
	if !store.IsTrusted("dev_a") {
		t.Fatal("device should be trusted")
	}
	devices, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].DeviceID != "dev_a" {
		t.Fatalf("unexpected devices: %+v", devices)
	}
	if err := store.Revoke("dev_a"); err != nil {
		t.Fatal(err)
	}
	if store.IsTrusted("dev_a") {
		t.Fatal("device should be revoked")
	}
}
