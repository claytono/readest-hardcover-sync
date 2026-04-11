package sync

import (
	"math"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
)

// ConvertProgress converts Readest page progress to Hardcover page count.
func ConvertProgress(readestCurrent, readestTotal, hardcoverPages int) int {
	if readestTotal == 0 || hardcoverPages == 0 {
		return 0
	}
	pages := int(math.Round(float64(readestCurrent) / float64(readestTotal) * float64(hardcoverPages)))
	if readestCurrent > 0 && pages < 1 {
		return 1
	}
	return pages
}

// DeriveStatus maps Readest reading state to Hardcover status_id.
// Returns StatusNone if no status update should be made.
//
// minPercent and minPages set thresholds before a book is considered "currently
// reading" or "read" based on progress alone. The book must reach minPercent% OR
// minPages (whichever comes first) to trigger Currently Reading. The same
// thresholds are mirrored at the end: progress-based completion requires reaching
// (100 - minPercent)% OR being within minPages of the end. Page-based thresholds
// are skipped for books shorter than minPages. Explicit "reading" status from
// Readest bypasses the start threshold; explicit "finished" bypasses all thresholds.
func DeriveStatus(readestCurrent, readestTotal int, readestStatus string, minPercent float64, minPages int) int {
	if readestStatus == "finished" {
		return hardcover.StatusRead
	}
	if readestTotal > 0 && readestCurrent >= readestTotal {
		return hardcover.StatusRead
	}
	if readestTotal > 0 && meetsEndThreshold(readestCurrent, readestTotal, minPercent, minPages) {
		return hardcover.StatusRead
	}
	if readestStatus == "reading" {
		return hardcover.StatusCurrentlyReading
	}
	if readestCurrent > 0 && meetsMinThreshold(readestCurrent, readestTotal, minPercent, minPages) {
		return hardcover.StatusCurrentlyReading
	}
	return hardcover.StatusNone
}

// IsAllowedTransition returns true if moving from currentStatus to newStatus is
// a valid forward transition. Only none/want-to-read → reading and
// none/reading → read are allowed. Paused, DNF, Ignored, and Read block updates
// since those represent intentional user decisions on Hardcover.
func IsAllowedTransition(currentStatus, newStatus int) bool {
	switch newStatus {
	case hardcover.StatusCurrentlyReading:
		return currentStatus == hardcover.StatusNone || currentStatus == hardcover.StatusWantToRead
	case hardcover.StatusRead:
		return currentStatus == hardcover.StatusNone || currentStatus == hardcover.StatusCurrentlyReading
	default:
		return false
	}
}

// meetsMinThreshold returns true if progress meets either the percentage or
// page count threshold. Page-based threshold is skipped for books shorter than
// minPages to avoid nonsensical gating on very short books.
func meetsMinThreshold(current, total int, minPercent float64, minPages int) bool {
	if total > minPages && current >= minPages {
		return true
	}
	if total > 0 && float64(current)/float64(total)*100 >= minPercent {
		return true
	}
	return false
}

// meetsEndThreshold returns true if progress is close enough to the end to be
// considered finished. Mirrors meetsMinThreshold: within minPages of the end
// OR at (100 - minPercent)% or higher. Page-based threshold is skipped for books
// shorter than minPages, and requires current > 0 to avoid marking unread books.
func meetsEndThreshold(current, total int, minPercent float64, minPages int) bool {
	remaining := total - current
	if remaining <= 0 {
		return true
	}
	if current > 0 && total > minPages && minPages > 0 && remaining <= minPages {
		return true
	}
	if minPercent > 0 && total > 0 && float64(current)/float64(total)*100 >= (100-minPercent) {
		return true
	}
	return false
}
