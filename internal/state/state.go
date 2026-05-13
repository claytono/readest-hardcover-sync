package state

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// diskState is the JSON-serializable representation of State.
type diskState struct {
	LastBookSync   int64                `json:"last_book_sync"`
	LastConfigSync int64                `json:"last_config_sync"`
	Books          map[string]BookState `json:"books"`
}

type State struct {
	mu             sync.RWMutex
	path           string
	lastBookSync   int64
	lastConfigSync int64
	lastSyncRanAt  time.Time // wall clock of last completed sync; not persisted
	Books          map[string]BookState
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
	UserBookID        int `json:"user_book_id,omitempty"`
	UserBookEditionID int `json:"user_book_edition_id,omitempty"`
	UserBookReadID    int `json:"user_book_read_id,omitempty"`

	// Last values sent (skip redundant updates)
	LastStatusSent   int `json:"last_status_sent,omitempty"`
	LastProgressSent int `json:"last_progress_sent,omitempty"`

	// Last seen Readest data
	ReadestProgress [2]int `json:"readest_progress,omitempty"`
	ReadestStatus   string `json:"readest_status,omitempty"`

	// Display metadata
	Series    string `json:"series,omitempty"`     // e.g., "Dungeon Crawler Carl #7"
	CoverURL  string `json:"cover_url,omitempty"`  // source cover image URL from Hardcover
	CoverPath string `json:"cover_path,omitempty"` // local path to cached cover image

	// Activity tracking for sort order
	LastActivityAt int64 `json:"last_activity_at,omitempty"` // Unix ms from config UpdatedAt

	Unmatched bool   `json:"unmatched,omitempty"`
	LastError string `json:"last_error,omitempty"`

	// Raw metadata from Readest for identifier parsing in the web UI.
	Metadata *string `json:"metadata,omitempty"`
}

func New(path string) *State {
	return &State{path: path, Books: make(map[string]BookState)}
}

func (s *State) GetLastBookSync() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBookSync
}

func (s *State) SetLastBookSync(ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBookSync = ts
}

func (s *State) GetLastConfigSync() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastConfigSync
}

func (s *State) SetLastConfigSync(ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastConfigSync = ts
}

func (s *State) ResetSyncTimestamps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBookSync = 0
	s.lastConfigSync = 0
}

func (s *State) GetLastSyncRanAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSyncRanAt
}

func (s *State) SetLastSyncRanAt(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSyncRanAt = t
}

func (s *State) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var ds diskState
	if err := json.Unmarshal(data, &ds); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBookSync = ds.LastBookSync
	s.lastConfigSync = ds.LastConfigSync
	s.Books = ds.Books
	if s.Books == nil {
		s.Books = make(map[string]BookState)
	}

	return nil
}

func (s *State) Save() error {
	if s.path == "" {
		return nil
	}
	s.mu.RLock()
	ds := diskState{
		LastBookSync:   s.lastBookSync,
		LastConfigSync: s.lastConfigSync,
		Books:          s.Books,
	}
	data, err := json.MarshalIndent(ds, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}

// UpdateBook acquires the lock, fetches the book, calls fn with a pointer to
// the book, and writes it back. Returns false if the book doesn't exist.
func (s *State) UpdateBook(hash string, fn func(*BookState)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.Books[hash]
	if !ok {
		return false
	}
	fn(&b)
	s.Books[hash] = b
	return true
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
	b.Series = ""
	b.CoverURL = ""
	b.CoverPath = ""
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
