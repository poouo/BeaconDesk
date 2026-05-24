package trust

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Device struct {
	DeviceID   string `json:"device_id"`
	Mode       string `json:"mode"`
	AddedAt    int64  `json:"added_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

type Store struct {
	path string
}

type fileData struct {
	Devices []Device `json:"devices"`
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) IsTrusted(deviceID string) bool {
	devices, err := s.List()
	if err != nil {
		return false
	}
	for _, device := range devices {
		if device.DeviceID == deviceID {
			return true
		}
	}
	return false
}

func (s *Store) Remember(deviceID string, mode string) error {
	if deviceID == "" {
		return errors.New("device id is required")
	}
	data, err := s.load()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for i := range data.Devices {
		if data.Devices[i].DeviceID == deviceID {
			data.Devices[i].Mode = mode
			data.Devices[i].LastUsedAt = now
			return s.save(data)
		}
	}
	data.Devices = append(data.Devices, Device{
		DeviceID:   deviceID,
		Mode:       mode,
		AddedAt:    now,
		LastUsedAt: now,
	})
	return s.save(data)
}

func (s *Store) Touch(deviceID string) error {
	data, err := s.load()
	if err != nil {
		return err
	}
	for i := range data.Devices {
		if data.Devices[i].DeviceID == deviceID {
			data.Devices[i].LastUsedAt = time.Now().UnixMilli()
			return s.save(data)
		}
	}
	return nil
}

func (s *Store) Revoke(deviceID string) error {
	data, err := s.load()
	if err != nil {
		return err
	}
	out := data.Devices[:0]
	for _, device := range data.Devices {
		if device.DeviceID != deviceID {
			out = append(out, device)
		}
	}
	data.Devices = out
	return s.save(data)
}

func (s *Store) List() ([]Device, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	devices := append([]Device(nil), data.Devices...)
	sort.Slice(devices, func(i int, j int) bool {
		return devices[i].LastUsedAt > devices[j].LastUsedAt
	})
	return devices, nil
}

func (s *Store) load() (fileData, error) {
	if s == nil || s.path == "" {
		return fileData{}, nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fileData{}, nil
	}
	if err != nil {
		return fileData{}, err
	}
	var data fileData
	if err := json.Unmarshal(b, &data); err != nil {
		return fileData{}, err
	}
	return data, nil
}

func (s *Store) save(data fileData) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
