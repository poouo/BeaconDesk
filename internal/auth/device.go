package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type DeviceIdentity struct {
	DeviceID     string `json:"device_id"`
	DeviceSecret string `json:"device_secret"`
	DeviceName   string `json:"device_name"`
}

func LoadOrCreateDeviceIdentity(path string, deviceName string) (DeviceIdentity, error) {
	if path == "" {
		return DeviceIdentity{}, errors.New("identity path is required")
	}

	if b, err := os.ReadFile(path); err == nil {
		var identity DeviceIdentity
		if err := json.Unmarshal(b, &identity); err != nil {
			return DeviceIdentity{}, err
		}
		if identity.DeviceID == "" || identity.DeviceSecret == "" {
			return DeviceIdentity{}, errors.New("identity file is missing required fields")
		}
		if deviceName != "" && identity.DeviceName != deviceName {
			identity.DeviceName = deviceName
			if err := SaveDeviceIdentity(path, identity); err != nil {
				return DeviceIdentity{}, err
			}
		}
		return identity, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return DeviceIdentity{}, err
	}

	if strings.TrimSpace(deviceName) == "" {
		deviceName = "BeaconDesk-device"
	}
	identity := DeviceIdentity{
		DeviceID:     "dev_" + randomHex(12),
		DeviceSecret: randomHex(32),
		DeviceName:   deviceName,
	}
	return identity, SaveDeviceIdentity(path, identity)
}

func SaveDeviceIdentity(path string, identity DeviceIdentity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
