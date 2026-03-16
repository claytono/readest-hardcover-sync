package readest

import "encoding/json"

// SyncResult is the response from GET /api/sync
type SyncResult struct {
	Books   []DBBook       `json:"books"`
	Configs []DBBookConfig `json:"configs"`
}

// DBBook represents a book record from the Readest sync API.
type DBBook struct {
	UserID        string  `json:"user_id"`
	BookHash      string  `json:"book_hash"`
	MetaHash      string  `json:"meta_hash,omitempty"`
	Format        string  `json:"format"`
	Title         string  `json:"title"`
	Author        string  `json:"author"`
	Progress      *[2]int `json:"progress,omitempty"`
	ReadingStatus string  `json:"reading_status,omitempty"`
	Metadata      *string `json:"metadata,omitempty"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
	DeletedAt     *string `json:"deleted_at,omitempty"`
}

// DBBookConfig represents a book config record.
// Progress is a JSON string containing "[current,total]" — NOT a JSON array.
type DBBookConfig struct {
	UserID    string  `json:"user_id"`
	BookHash  string  `json:"book_hash"`
	MetaHash  string  `json:"meta_hash,omitempty"`
	Progress  string  `json:"progress,omitempty"`
	UpdatedAt string  `json:"updated_at,omitempty"`
	DeletedAt *string `json:"deleted_at,omitempty"`
}

// ParseConfigProgress extracts [current, total] from a JSON-stringified progress.
func ParseConfigProgress(s string) ([2]int, error) {
	var arr [2]int
	if s == "" {
		return arr, nil
	}
	err := json.Unmarshal([]byte(s), &arr)
	return arr, err
}

const DummyBookHash = "00000000000000000000000000000000"

func (b *DBBook) IsDummyBook() bool {
	return b.BookHash == DummyBookHash
}
