package identifier

import (
	"encoding/json"
	"strings"
)

// ParsedIdentifiers holds the structured identifiers extracted from book metadata.
type ParsedIdentifiers struct {
	HardcoverSlugs      []string
	HardcoverEditionIDs []string
	ISBN13s             []string
	ISBN10s             []string
	ASINs               []string
	Title               string
	Author              string
	Series              string
	SeriesIndex         float64
}

// metadataDoc is used to unmarshal the top-level metadata JSON.
type metadataDoc struct {
	AltIdentifier json.RawMessage `json:"altIdentifier"`
	Identifier    string          `json:"identifier"`
	Series        string          `json:"series"`
	SeriesIndex   float64         `json:"seriesIndex"`
}

// schemeObject represents a {scheme, value} identifier object.
type schemeObject struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}

// Parse extracts structured identifiers from the given metadata JSON string.
// title and author are always set on the result regardless of metadata content.
func Parse(metadataJSON *string, title, author string) ParsedIdentifiers {
	result := ParsedIdentifiers{
		Title:  title,
		Author: author,
	}

	if metadataJSON == nil {
		return result
	}

	var doc metadataDoc
	if err := json.Unmarshal([]byte(*metadataJSON), &doc); err != nil {
		return result
	}

	result.Series = doc.Series
	result.SeriesIndex = doc.SeriesIndex

	// Collect raw identifier tokens from altIdentifier or fallback to identifier.
	if len(doc.AltIdentifier) > 0 && string(doc.AltIdentifier) != "null" {
		parseAltIdentifier(doc.AltIdentifier, &result)
	} else if doc.Identifier != "" {
		parseStringIdentifier(doc.Identifier, &result)
	}

	return result
}

// parseAltIdentifier handles the altIdentifier field which may be a string,
// an array of mixed elements, or a single {scheme,value} object.
func parseAltIdentifier(raw json.RawMessage, result *ParsedIdentifiers) {
	s := strings.TrimSpace(string(raw))

	switch s[0] {
	case '"':
		// Single string value.
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			parseStringIdentifier(str, result)
		}
	case '[':
		// Array of mixed elements.
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return
		}
		for _, item := range items {
			parseRawItem(item, result)
		}
	case '{':
		// Single {scheme, value} object.
		parseObjectIdentifier(raw, result)
	}
}

// parseRawItem dispatches a single array element by its JSON type.
func parseRawItem(raw json.RawMessage, result *ParsedIdentifiers) {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return
	}
	switch s[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			parseStringIdentifier(str, result)
		}
	case '{':
		parseObjectIdentifier(raw, result)
	}
}

// parseObjectIdentifier handles {scheme, value} objects.
func parseObjectIdentifier(raw json.RawMessage, result *ParsedIdentifiers) {
	var obj schemeObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	if strings.ToUpper(obj.Scheme) == "HARDCOVER" {
		if obj.Value != "" {
			result.HardcoverSlugs = append(result.HardcoverSlugs, obj.Value)
		}
	}
}

// parseStringIdentifier parses a string identifier and populates the result.
func parseStringIdentifier(s string, result *ParsedIdentifiers) {
	switch {
	case strings.HasPrefix(s, "hardcover-edition:"):
		val := strings.TrimPrefix(s, "hardcover-edition:")
		if val != "" {
			result.HardcoverEditionIDs = append(result.HardcoverEditionIDs, val)
		}
	case strings.HasPrefix(s, "hardcover-slug:"):
		val := strings.TrimPrefix(s, "hardcover-slug:")
		if val != "" {
			result.HardcoverSlugs = append(result.HardcoverSlugs, val)
		}
	case strings.HasPrefix(s, "hardcover:"):
		val := strings.TrimPrefix(s, "hardcover:")
		if val != "" {
			result.HardcoverSlugs = append(result.HardcoverSlugs, val)
		}
	case strings.HasPrefix(strings.ToLower(s), "urn:isbn:"):
		val := s[len("urn:isbn:"):]
		classifyISBN(val, result)
	case strings.HasPrefix(s, "mobi-asin:"):
		val := strings.TrimPrefix(s, "mobi-asin:")
		if val != "" {
			result.ASINs = append(result.ASINs, strings.ToUpper(val))
		}
	default:
		// Try bare digits (possibly with hyphens) as ISBN.
		classifyBareISBN(s, result)
	}
}

// classifyISBN strips hyphens and classifies based on digit count.
func classifyISBN(s string, result *ParsedIdentifiers) {
	digits := strings.ReplaceAll(s, "-", "")
	switch len(digits) {
	case 13:
		result.ISBN13s = append(result.ISBN13s, digits)
	case 10:
		result.ISBN10s = append(result.ISBN10s, digits)
	}
}

// classifyBareISBN checks if s (after removing hyphens) is all digits and classifies.
func classifyBareISBN(s string, result *ParsedIdentifiers) {
	digits := strings.ReplaceAll(s, "-", "")
	if !isAllDigits(digits) {
		return
	}
	switch len(digits) {
	case 13:
		result.ISBN13s = append(result.ISBN13s, digits)
	case 10:
		result.ISBN10s = append(result.ISBN10s, digits)
	}
}

// isAllDigits returns true if every character in s is an ASCII digit.
func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
