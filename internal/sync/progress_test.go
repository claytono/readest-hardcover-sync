package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertProgress(t *testing.T) {
	tests := []struct {
		name           string
		current        int
		total          int
		hardcoverPages int
		want           int
	}{
		{"half way through", 350, 700, 400, 200},
		{"first page clamps to 1", 1, 700, 400, 1},
		{"zero total returns 0", 0, 0, 400, 0},
		{"zero hardcover pages returns 0", 350, 700, 0, 0},
		{"finished returns full pages", 700, 700, 400, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertProgress(tt.current, tt.total, tt.hardcoverPages)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeriveStatus(t *testing.T) {
	tests := []struct {
		name    string
		current int
		total   int
		status  string
		want    int
	}{
		{"reading in progress", 350, 700, "reading", 2},
		{"explicit finished", 700, 700, "finished", 3},
		{"100 percent complete is finished", 700, 700, "reading", 3},
		{"unread returns 0", 0, 700, "unread", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStatus(tt.current, tt.total, tt.status)
			assert.Equal(t, tt.want, got)
		})
	}
}
