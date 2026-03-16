package readest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/readest"
)

func TestParseConfigProgress_Valid(t *testing.T) {
	arr, err := readest.ParseConfigProgress("[350,700]")
	require.NoError(t, err)
	assert.Equal(t, [2]int{350, 700}, arr)
}

func TestParseConfigProgress_Empty(t *testing.T) {
	arr, err := readest.ParseConfigProgress("")
	require.NoError(t, err)
	assert.Equal(t, [2]int{0, 0}, arr)
}

func TestParseConfigProgress_Invalid(t *testing.T) {
	_, err := readest.ParseConfigProgress("not json")
	assert.Error(t, err)
}

func TestDBBook_IsDummyBook(t *testing.T) {
	dummy := readest.DBBook{BookHash: readest.DummyBookHash}
	assert.True(t, dummy.IsDummyBook())

	real := readest.DBBook{BookHash: "aaaa1111bbbb2222cccc3333dddd4444"}
	assert.False(t, real.IsDummyBook())
}
