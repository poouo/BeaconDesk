package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type RelayConfig struct {
	Listen                 string
	Transport              string
	WebSocketPath          string
	WebControlEnabled      bool
	WebControlPath         string
	WebListen              string
	PublicBaseURL          string
	SharedToken            string
	TLSCertFile            string
	TLSKeyFile             string
	HeartbeatTimeout       time.Duration
	MaxClients             int
	BandwidthLimitKbps     int
	AllowInsecurePlaintext bool
}

func DefaultRelayConfig() RelayConfig {
	return RelayConfig{
		Listen:                 ":8443",
		Transport:              "tcp",
		WebSocketPath:          "/ws",
		WebControlEnabled:      true,
		WebControlPath:         "/web",
		WebListen:              ":8080",
		HeartbeatTimeout:       45 * time.Second,
		MaxClients:             1000,
		BandwidthLimitKbps:     4096,
		AllowInsecurePlaintext: true,
	}
}

// LoadRelayConfig reads a small key=value config file. The MVP intentionally
// avoids external dependencies; YAML support can be added without changing the
// public RelayConfig shape.
func LoadRelayConfig(path string) (RelayConfig, error) {
	cfg := DefaultRelayConfig()
	if path == "" {
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("invalid config line: %q", line)
		}
		key = strings.TrimPrefix(strings.TrimSpace(key), "\ufeff")
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "listen":
			cfg.Listen = value
		case "transport":
			cfg.Transport = strings.ToLower(value)
		case "websocket_path":
			cfg.WebSocketPath = value
		case "web_control_enabled":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return cfg, err
			}
			cfg.WebControlEnabled = b
		case "web_control_path":
			cfg.WebControlPath = value
		case "web_listen":
			cfg.WebListen = value
		case "public_base_url":
			cfg.PublicBaseURL = strings.TrimRight(value, "/")
		case "shared_token":
			cfg.SharedToken = value
		case "tls_cert_file":
			cfg.TLSCertFile = value
		case "tls_key_file":
			cfg.TLSKeyFile = value
		case "heartbeat_timeout_seconds":
			n, err := strconv.Atoi(value)
			if err != nil {
				return cfg, err
			}
			cfg.HeartbeatTimeout = time.Duration(n) * time.Second
		case "max_clients":
			n, err := strconv.Atoi(value)
			if err != nil {
				return cfg, err
			}
			cfg.MaxClients = n
		case "bandwidth_limit_kbps":
			n, err := strconv.Atoi(value)
			if err != nil {
				return cfg, err
			}
			cfg.BandwidthLimitKbps = n
		case "allow_insecure_plaintext":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return cfg, err
			}
			cfg.AllowInsecurePlaintext = b
		default:
			return cfg, fmt.Errorf("unknown config key %q", key)
		}
	}
	return cfg, scanner.Err()
}
