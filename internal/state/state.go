package state

import (
	"encoding/json"
	"os"
	"sync"
)

type State struct {
	mu             sync.RWMutex         `json:"-"`
	path           string               `json:"-"`
	LastBookSync   int64                `json:"last_book_sync"`
	LastConfigSync int64                `json:"last_config_sync"`
	Books          map[string]BookState `json:"books"`
}

type BookState struct {
	BookHash string `json:"book_hash"`
	Title    string `json:"title"`
	Author   string `json:"author"`

	// Hardcover mapping
	HardcoverBookID int    `json:"hardcover_book_id,omitempty"`
	HardcoverSlug   string `json:"hardcover_slug,omitempty"`
	EditionID       int    `json:"edition_id,omitempty"`
	EditionPages    int    `json:"edition_pages,omitempty"`
	ReadingFormatID int    `json:"reading_format_id,omitempty"`
	MatchMethod     string `json:"match_method,omitempty"`

	// Hardcover IDs for updates
	UserBookID     int `json:"user_book_id,omitempty"`
	UserBookReadID int `json:"user_book_read_id,omitempty"`

	// Last values sent (skip redundant updates)
	LastStatusSent   int `json:"last_status_sent,omitempty"`
	LastProgressSent int `json:"last_progress_sent,omitempty"`

	// Last seen Readest data
	ReadestProgress [2]int `json:"readest_progress,omitempty"`
	ReadestStatus   string `json:"readest_status,omitempty"`

	Unmatched bool   `json:"unmatched,omitempty"`
	LastError string `json:"last_error,omitempty"`

	// Raw metadata from Readest for identifier parsing in the web UI.
	Metadata *string `json:"metadata,omitempty"`
}

func New(path string) *State {
	return &State{path: path, Books: make(map[string]BookState)}
}

func (s *State) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if err := json.Unmarshal(data, s); err != nil {
		return err
	}

	if s.Books == nil {
		s.Books = make(map[string]BookState)
	}

	return nil
}

func (s *State) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}

func (s *State) GetBook(hash string) (BookState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.Books[hash]
	return b, ok
}

func (s *State) SetBook(hash string, b BookState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Books[hash] = b
}

func (s *State) SetManualLink(hash string, bookID int, slug string, editionID, editionPages int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.Books[hash]
	b.HardcoverBookID = bookID
	b.HardcoverSlug = slug
	b.EditionID = editionID
	b.EditionPages = editionPages
	b.MatchMethod = "manual"
	b.Unmatched = false
	s.Books[hash] = b
}

func (s *State) IsMatched(hash string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.Books[hash]
	return ok && b.HardcoverBookID != 0
}

func (s *State) ListBooks() []BookState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	books := make([]BookState, 0, len(s.Books))
	for _, b := range s.Books {
		books = append(books, b)
	}
	return books
}
