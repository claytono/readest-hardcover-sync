package sync

import "math"

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
// Returns 0 if no status update should be made.
// 2 = Currently Reading, 3 = Read.
func DeriveStatus(readestCurrent, readestTotal int, readestStatus string) int {
	if readestStatus == "finished" || (readestTotal > 0 && readestCurrent >= readestTotal) {
		return 3
	}
	if readestStatus == "reading" || readestCurrent > 0 {
		return 2
	}
	return 0
}
