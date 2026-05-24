package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const defaultMaxEntries = 500

type Entry struct {
	Time      int64  `json:"time"`
	Event     string `json:"event"`
	LocalID   string `json:"local_id,omitempty"`
	PeerID    string `json:"peer_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type Store struct {
	path       string
	maxEntries int
}

type fileData struct {
	Entries []Entry `json:"entries"`
}

func NewStore(path string) *Store {
	return &Store{path: path, maxEntries: defaultMaxEntries}
}

func (s *Store) Append(entry Entry) error {
	if s == nil || s.path == "" {
		return nil
	}
	if entry.Time == 0 {
		entry.Time = time.Now().UnixMilli()
	}
	data, err := s.load()
	if err != nil {
		return err
	}
	data.Entries = append(data.Entries, entry)
	limit := s.maxEntries
	if limit <= 0 {
		limit = defaultMaxEntries
	}
	if len(data.Entries) > limit {
		data.Entries = data.Entries[len(data.Entries)-limit:]
	}
	return s.save(data)
}

func (s *Store) List(limit int) ([]Entry, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	type indexedEntry struct {
		entry Entry
		index int
	}
	indexed := make([]indexedEntry, 0, len(data.Entries))
	for i, entry := range data.Entries {
		indexed = append(indexed, indexedEntry{entry: entry, index: i})
	}
	sort.SliceStable(indexed, func(i int, j int) bool {
		if indexed[i].entry.Time == indexed[j].entry.Time {
			return indexed[i].index > indexed[j].index
		}
		return indexed[i].entry.Time > indexed[j].entry.Time
	})
	entries := make([]Entry, 0, len(indexed))
	for _, item := range indexed {
		entries = append(entries, item.entry)
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *Store) Clear() error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, []byte("{\n  \"entries\": []\n}\n"), 0o600)
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
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
