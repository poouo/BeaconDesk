package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRelayConfigTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.conf")
	data := []byte(`
listen = ":9443"
transport = "websocket"
websocket_path = "/relay"
web_control_enabled = true
web_control_path = "/assist"
web_listen = ":9080"
public_base_url = "https://relay.example.com"
shared_token = "secret"
tls_cert_file = "/etc/beacondesk/tls/fullchain.pem"
tls_key_file = "/etc/beacondesk/tls/privkey.pem"
allow_insecure_plaintext = false
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadRelayConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9443" {
		t.Fatalf("Listen = %q", cfg.Listen)
	}
	if cfg.SharedToken != "secret" {
		t.Fatalf("SharedToken = %q", cfg.SharedToken)
	}
	if cfg.Transport != "websocket" {
		t.Fatalf("Transport = %q", cfg.Transport)
	}
	if cfg.WebSocketPath != "/relay" {
		t.Fatalf("WebSocketPath = %q", cfg.WebSocketPath)
	}
	if !cfg.WebControlEnabled {
		t.Fatal("WebControlEnabled should be true")
	}
	if cfg.WebControlPath != "/assist" {
		t.Fatalf("WebControlPath = %q", cfg.WebControlPath)
	}
	if cfg.WebListen != ":9080" {
		t.Fatalf("WebListen = %q", cfg.WebListen)
	}
	if cfg.PublicBaseURL != "https://relay.example.com" {
		t.Fatalf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
	if cfg.TLSCertFile != "/etc/beacondesk/tls/fullchain.pem" {
		t.Fatalf("TLSCertFile = %q", cfg.TLSCertFile)
	}
	if cfg.TLSKeyFile != "/etc/beacondesk/tls/privkey.pem" {
		t.Fatalf("TLSKeyFile = %q", cfg.TLSKeyFile)
	}
	if cfg.AllowInsecurePlaintext {
		t.Fatal("AllowInsecurePlaintext should be false")
	}
}
