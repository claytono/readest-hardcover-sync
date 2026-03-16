package identifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/claytono/readest-hardcover-sync/internal/identifier"
)

func strPtr(s string) *string {
	return &s
}

func TestParse_BookloreHardcoverSlug(t *testing.T) {
	metadata := `{"altIdentifier":[{"scheme":"HARDCOVER","value":"the-gate-of-the-feral-gods"}]}`
	result := identifier.Parse(strPtr(metadata), "The Gate", "Matt Dinniman")
	assert.Equal(t, []string{"the-gate-of-the-feral-gods"}, result.HardcoverSlugs)
	assert.Empty(t, result.ISBN13s)
	assert.Empty(t, result.ASINs)
}

func TestParse_KOReaderHardcoverSlug(t *testing.T) {
	metadata := `{"altIdentifier":"hardcover:the-gate-of-the-feral-gods"}`
	result := identifier.Parse(strPtr(metadata), "The Gate", "Matt Dinniman")
	assert.Equal(t, []string{"the-gate-of-the-feral-gods"}, result.HardcoverSlugs)
}

func TestParse_KOReaderHardcoverSlugDash(t *testing.T) {
	metadata := `{"altIdentifier":"hardcover-slug:the-gate-of-the-feral-gods"}`
	result := identifier.Parse(strPtr(metadata), "The Gate", "Matt Dinniman")
	assert.Equal(t, []string{"the-gate-of-the-feral-gods"}, result.HardcoverSlugs)
}

func TestParse_ISBN13(t *testing.T) {
	metadata := `{"altIdentifier":"urn:ISBN:9798217287192"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"9798217287192"}, result.ISBN13s)
	assert.Empty(t, result.ISBN10s)
}

func TestParse_ISBN13WithHyphens(t *testing.T) {
	metadata := `{"altIdentifier":"urn:ISBN:978-0-06-112008-4"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"9780061120084"}, result.ISBN13s)
	assert.Empty(t, result.ISBN10s)
}

func TestParse_ISBN10WithHyphens(t *testing.T) {
	metadata := `{"altIdentifier":"urn:ISBN:0-06-112008-4"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"0061120084"}, result.ISBN10s)
	assert.Empty(t, result.ISBN13s)
}

func TestParse_ASIN(t *testing.T) {
	metadata := `{"altIdentifier":"mobi-asin:B093DJ7F3C"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"B093DJ7F3C"}, result.ASINs)
}

func TestParse_NilMetadata(t *testing.T) {
	result := identifier.Parse(nil, "My Title", "My Author")
	assert.Empty(t, result.HardcoverSlugs)
	assert.Empty(t, result.HardcoverEditionIDs)
	assert.Empty(t, result.ISBN13s)
	assert.Empty(t, result.ISBN10s)
	assert.Empty(t, result.ASINs)
	assert.Equal(t, "My Title", result.Title)
	assert.Equal(t, "My Author", result.Author)
}

func TestParse_RealWorldExample(t *testing.T) {
	metadata := `{"title":"The Gate of the Feral Gods","author":"Matt Dinniman","identifier":"uuid:cafad31b-ba8b-4511-a5b0-60226d07768b","altIdentifier":["mobi-asin:B093DJ7F3C","amazon:B093DJ7F3C","calibre:298","uuid:f7046f95-b7f4-498e-a5b3-ea2ab514daeb",{"scheme":"HARDCOVER","value":"the-gate-of-the-feral-gods"},"urn:ISBN:9798217287192"],"series":"Dungeon Crawler Carl","seriesIndex":4}`
	result := identifier.Parse(strPtr(metadata), "The Gate of the Feral Gods", "Matt Dinniman")
	assert.Equal(t, []string{"the-gate-of-the-feral-gods"}, result.HardcoverSlugs)
	assert.Equal(t, []string{"9798217287192"}, result.ISBN13s)
	assert.Equal(t, []string{"B093DJ7F3C"}, result.ASINs)
	assert.Empty(t, result.ISBN10s)
	assert.Empty(t, result.HardcoverEditionIDs)
	assert.Equal(t, "Dungeon Crawler Carl", result.Series)
	assert.Equal(t, float64(4), result.SeriesIndex)
}

func TestParse_FallbackToIdentifier(t *testing.T) {
	metadata := `{"identifier":"urn:ISBN:9798217287192"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"9798217287192"}, result.ISBN13s)
}

func TestParse_TitleAndAuthor(t *testing.T) {
	metadata := `{"altIdentifier":[]}`
	result := identifier.Parse(strPtr(metadata), "My Title", "My Author")
	assert.Equal(t, "My Title", result.Title)
	assert.Equal(t, "My Author", result.Author)
}

func TestParse_HardcoverEditionID(t *testing.T) {
	metadata := `{"altIdentifier":"hardcover-edition:12345"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"12345"}, result.HardcoverEditionIDs)
	assert.Empty(t, result.HardcoverSlugs)
}

func TestParse_BareISBN13(t *testing.T) {
	metadata := `{"altIdentifier":"9798217287192"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"9798217287192"}, result.ISBN13s)
}

func TestParse_BareISBN10(t *testing.T) {
	metadata := `{"altIdentifier":"0061120084"}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"0061120084"}, result.ISBN10s)
}

func TestParse_IgnoredIdentifiers(t *testing.T) {
	metadata := `{"altIdentifier":["calibre:298","uuid:f7046f95-b7f4-498e-a5b3-ea2ab514daeb","amazon:B093DJ7F3C"]}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Empty(t, result.HardcoverSlugs)
	assert.Empty(t, result.ISBN13s)
	assert.Empty(t, result.ISBN10s)
	assert.Empty(t, result.ASINs)
}

func TestParse_SchemeObjectCaseInsensitive(t *testing.T) {
	metadata := `{"altIdentifier":{"scheme":"hardcover","value":"some-slug"}}`
	result := identifier.Parse(strPtr(metadata), "Some Book", "Some Author")
	assert.Equal(t, []string{"some-slug"}, result.HardcoverSlugs)
}
