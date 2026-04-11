package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
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
		{"reading in progress", 350, 700, "reading", hardcover.StatusCurrentlyReading},
		{"explicit finished", 700, 700, "finished", hardcover.StatusRead},
		{"100 percent complete is finished", 700, 700, "reading", hardcover.StatusRead},
		{"unread returns none", 0, 700, "unread", hardcover.StatusNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStatus(tt.current, tt.total, tt.status, 2, 5)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDeriveStatus_CurrentPositiveNoStatus: current > 0 with no explicit reading
// status — should be treated as "currently reading" when above threshold.
func TestDeriveStatus_CurrentPositiveNoStatus(t *testing.T) {
	got := DeriveStatus(50, 400, "", 2, 5)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "positive current above threshold should be Currently Reading")
}

// TestDeriveStatus_FinishedStatusWithLowProgress: "finished" status even when
// current < total — should still return Read.
func TestDeriveStatus_FinishedStatusWithLowProgress(t *testing.T) {
	got := DeriveStatus(100, 400, "finished", 2, 5)
	assert.Equal(t, hardcover.StatusRead, got, "finished status overrides progress position")
}

func TestDeriveStatus_BelowThreshold(t *testing.T) {
	// 1 of 356 = 0.28%, below 2% and below 5 pages
	got := DeriveStatus(1, 356, "", 2, 5)
	assert.Equal(t, hardcover.StatusNone, got, "progress below both thresholds should return StatusNone")
}

func TestDeriveStatus_MeetsPageThreshold(t *testing.T) {
	// 5 of 500 = 1%, below 2% but meets 5-page minimum
	got := DeriveStatus(5, 500, "", 2, 5)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "meeting page threshold should be Currently Reading")
}

func TestDeriveStatus_MeetsPercentThreshold(t *testing.T) {
	// 2 of 100 = 2%, meets 2% but below 5 pages
	got := DeriveStatus(2, 100, "", 2, 5)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "meeting percent threshold should be Currently Reading")
}

func TestDeriveStatus_ExplicitReadingBypassesThreshold(t *testing.T) {
	// 1 of 356, below thresholds but has explicit "reading" status
	got := DeriveStatus(1, 356, "reading", 2, 5)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "explicit reading status bypasses threshold")
}

func TestDeriveStatus_ZeroThresholdsDisableGate(t *testing.T) {
	got := DeriveStatus(1, 356, "", 0, 0)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "zero thresholds disable the gate — any progress syncs")
}

func TestDeriveStatus_ZeroCurrentAlwaysSkipped(t *testing.T) {
	got := DeriveStatus(0, 400, "", 0, 0)
	assert.Equal(t, hardcover.StatusNone, got, "zero current always returns StatusNone regardless of thresholds")
}

func TestDeriveStatus_NearEndMarksFinished(t *testing.T) {
	// 696 of 700 = within 5 pages of end
	got := DeriveStatus(696, 700, "", 2, 5)
	assert.Equal(t, hardcover.StatusRead, got, "within minPages of end should be Read")
}

func TestDeriveStatus_NearEndPercentMarksFinished(t *testing.T) {
	// 99 of 100 = 99%, above 98% threshold
	got := DeriveStatus(99, 100, "", 2, 5)
	assert.Equal(t, hardcover.StatusRead, got, "at 99% should be Read")
}

func TestDeriveStatus_NotCloseEnoughToEnd(t *testing.T) {
	// 690 of 700 = 10 pages from end, 98.6% — above percent threshold but not page
	// Actually 690/700 = 98.57% >= 98%, so this should be finished
	got := DeriveStatus(690, 700, "", 2, 5)
	assert.Equal(t, hardcover.StatusRead, got, "at 98.6% should be Read")
}

func TestDeriveStatus_PastEndMarksFinished(t *testing.T) {
	// current exceeds total (e.g., appendix pages) — should still be finished
	got := DeriveStatus(710, 700, "", 2, 5)
	assert.Equal(t, hardcover.StatusRead, got, "past end of book should be Read")
}

func TestDeriveStatus_MidBookStillReading(t *testing.T) {
	// 650 of 700 = 92.9%, well above start threshold but not near end
	got := DeriveStatus(650, 700, "", 2, 5)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "at 92.9% should still be Currently Reading")
}

func TestDeriveStatus_ShortBookOpenedNotFinished(t *testing.T) {
	// 4-page book, opened page 1 — shorter than minPages=5, so page thresholds
	// are skipped. 25% >= 2% so start threshold passes, but end threshold should
	// NOT fire (remaining=3 but total <= minPages skips page check, and 25% < 98%).
	got := DeriveStatus(1, 4, "", 2, 5)
	assert.Equal(t, hardcover.StatusCurrentlyReading, got, "opening a short book should be Currently Reading, not Read")
}

func TestDeriveStatus_ShortBookUnreadNotFinished(t *testing.T) {
	// 4-page book, current=0 — should not be marked as anything
	got := DeriveStatus(0, 4, "", 2, 5)
	assert.Equal(t, hardcover.StatusNone, got, "unread short book should not be synced")
}

func TestDeriveStatus_ShortBookAtEnd(t *testing.T) {
	// 4-page book at page 4 — current >= total, so finished regardless of thresholds
	got := DeriveStatus(4, 4, "", 2, 5)
	assert.Equal(t, hardcover.StatusRead, got, "short book at end should be Read")
}

func TestIsAllowedTransition(t *testing.T) {
	tests := []struct {
		name    string
		current int
		new     int
		want    bool
	}{
		{"no status to reading", hardcover.StatusNone, hardcover.StatusCurrentlyReading, true},
		{"want-to-read to reading", hardcover.StatusWantToRead, hardcover.StatusCurrentlyReading, true},
		{"reading to read", hardcover.StatusCurrentlyReading, hardcover.StatusRead, true},
		{"no status to read", hardcover.StatusNone, hardcover.StatusRead, true},
		{"paused to reading blocked", hardcover.StatusPaused, hardcover.StatusCurrentlyReading, false},
		{"dnf to reading blocked", hardcover.StatusDidNotFinish, hardcover.StatusCurrentlyReading, false},
		{"ignored to reading blocked", hardcover.StatusIgnored, hardcover.StatusCurrentlyReading, false},
		{"dnf to read blocked", hardcover.StatusDidNotFinish, hardcover.StatusRead, false},
		{"read to reading blocked", hardcover.StatusRead, hardcover.StatusCurrentlyReading, false},
		{"want-to-read to read blocked", hardcover.StatusWantToRead, hardcover.StatusRead, false},
		{"unknown new status blocked", hardcover.StatusCurrentlyReading, 99, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAllowedTransition(tt.current, tt.new)
			assert.Equal(t, tt.want, got)
		})
	}
}
