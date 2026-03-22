package demo

import (
	"crypto/sha256"
	"fmt"

	"github.com/claytono/readest-hardcover-sync/internal/state"
)

func bookHash(title string) string {
	h := sha256.Sum256([]byte("demo-" + title))
	return fmt.Sprintf("%x", h)[:16]
}

func strPtr(s string) *string {
	return &s
}

type demoBook struct {
	state    state.BookState
	metadata *string
}

func demoBooks() []demoBook {
	return []demoBook{
		{
			state: state.BookState{
				BookHash:         bookHash("Pride and Prejudice"),
				Title:            "Pride and Prejudice",
				Author:           "Jane Austen",
				HardcoverBookID:  379647,
				HardcoverSlug:    "pride-and-prejudice",
				EditionID:        15679503,
				EditionPages:     435,
				ReadingFormatID:  4,
				MatchMethod:      "slug",
				ReadestProgress:  [2]int{435, 435},
				ReadestStatus:    "finished",
				UserBookID:       1001,
				UserBookReadID:   2001,
				LastStatusSent:   3,
				LastProgressSent: 100,
				CoverPath:        "pride-and-prejudice.jpg",
				LastActivityAt:   1710000000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780141439518","altIdentifier":[{"scheme":"HARDCOVER","value":"pride-and-prejudice"},{"scheme":"ISBN","value":"9780141439518"}]}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("Moby Dick"),
				Title:            "Moby Dick",
				Author:           "Herman Melville",
				HardcoverBookID:  1788666,
				HardcoverSlug:    "moby-dick",
				EditionID:        30542720,
				EditionPages:     789,
				ReadingFormatID:  4,
				MatchMethod:      "isbn13",
				ReadestProgress:  [2]int{529, 789},
				ReadestStatus:    "reading",
				UserBookID:       1002,
				UserBookReadID:   2002,
				LastStatusSent:   2,
				LastProgressSent: 67,
				CoverPath:        "moby-dick.jpg",
				LastActivityAt:   1709900000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780142437247","altIdentifier":"9780142437247"}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("Frankenstein"),
				Title:            "Frankenstein",
				Author:           "Mary Shelley",
				HardcoverBookID:  28722,
				HardcoverSlug:    "frankenstein",
				EditionID:        22082016,
				EditionPages:     270,
				ReadingFormatID:  4,
				MatchMethod:      "isbn10",
				ReadestProgress:  [2]int{62, 270},
				ReadestStatus:    "reading",
				UserBookID:       1003,
				UserBookReadID:   2003,
				LastStatusSent:   2,
				LastProgressSent: 23,
				CoverPath:        "frankenstein.jpg",
				LastActivityAt:   1709800000000,
			},
			metadata: strPtr(`{"identifier":"0141439475","altIdentifier":"0141439475"}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("Dracula"),
				Title:            "Dracula",
				Author:           "Bram Stoker",
				HardcoverBookID:  428657,
				HardcoverSlug:    "dracula",
				EditionID:        12963649,
				EditionPages:     422,
				ReadingFormatID:  1,
				MatchMethod:      "title",
				ReadestProgress:  [2]int{422, 422},
				ReadestStatus:    "finished",
				UserBookID:       1004,
				UserBookReadID:   2004,
				LastStatusSent:   3,
				LastProgressSent: 100,
				CoverPath:        "dracula.jpg",
				LastActivityAt:   1709700000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780141439846","altIdentifier":"9780141439846"}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("A Study in Scarlet"),
				Title:            "A Study in Scarlet",
				Author:           "Arthur Conan Doyle",
				HardcoverBookID:  383924,
				HardcoverSlug:    "a-study-in-scarlet",
				EditionID:        31194157,
				EditionPages:     175,
				ReadingFormatID:  4,
				MatchMethod:      "slug",
				Series:           "Sherlock Holmes #1",
				ReadestProgress:  [2]int{21, 175},
				ReadestStatus:    "reading",
				UserBookID:       1005,
				UserBookReadID:   2005,
				LastStatusSent:   2,
				LastProgressSent: 12,
				CoverPath:        "a-study-in-scarlet.jpg",
				LastActivityAt:   1709600000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780141441115","altIdentifier":[{"scheme":"HARDCOVER","value":"a-study-in-scarlet"},{"scheme":"ISBN","value":"9780141441115"}]}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("A Tale of Two Cities"),
				Title:            "A Tale of Two Cities",
				Author:           "Charles Dickens",
				HardcoverBookID:  187436,
				HardcoverSlug:    "a-tale-of-two-cities",
				EditionID:        31450339,
				EditionPages:     460,
				ReadingFormatID:  4,
				MatchMethod:      "isbn13",
				ReadestProgress:  [2]int{460, 460},
				ReadestStatus:    "finished",
				UserBookID:       1006,
				UserBookReadID:   2006,
				LastStatusSent:   3,
				LastProgressSent: 100,
				CoverPath:        "a-tale-of-two-cities.jpg",
				LastActivityAt:   1709500000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780141439600","altIdentifier":"9780141439600"}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("Crime and Punishment"),
				Title:            "Crime and Punishment",
				Author:           "Fyodor Dostoevsky",
				HardcoverBookID:  469783,
				HardcoverSlug:    "crime-and-punishment",
				EditionID:        15010300,
				EditionPages:     656,
				ReadingFormatID:  4,
				MatchMethod:      "slug",
				ReadestProgress:  [2]int{262, 656},
				ReadestStatus:    "reading",
				UserBookID:       1007,
				UserBookReadID:   2007,
				LastStatusSent:   2,
				LastProgressSent: 40,
				CoverPath:        "crime-and-punishment.jpg",
				LastActivityAt:   1709400000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780140449136","altIdentifier":[{"scheme":"HARDCOVER","value":"crime-and-punishment"},{"scheme":"ISBN","value":"9780140449136"}]}`),
		},
		{
			state: state.BookState{
				BookHash:         bookHash("Jane Eyre"),
				Title:            "Jane Eyre",
				Author:           "Charlotte Brontë",
				HardcoverBookID:  386180,
				HardcoverSlug:    "jane-eyre",
				EditionID:        30913699,
				EditionPages:     564,
				ReadingFormatID:  4,
				MatchMethod:      "manual",
				ReadestProgress:  [2]int{0, 564},
				ReadestStatus:    "want-to-read",
				UserBookID:       1008,
				UserBookReadID:   0,
				LastStatusSent:   1,
				LastProgressSent: 0,
				CoverPath:        "jane-eyre.jpg",
				LastActivityAt:   1709300000000,
			},
			metadata: strPtr(`{"identifier":"urn:isbn:9780141441146","altIdentifier":"9780141441146"}`),
		},
		{
			state: state.BookState{
				BookHash:        bookHash("The War of the Worlds"),
				Title:           "The War of the Worlds",
				Author:          "H.G. Wells",
				ReadestProgress: [2]int{54, 180},
				ReadestStatus:   "reading",
				Unmatched:       true,
				LastActivityAt:  1709200000000,
			},
			metadata: nil,
		},
		{
			state: state.BookState{
				BookHash:       bookHash("The Adventures of Sherlock Holmes"),
				Title:          "The Adventures of Sherlock Holmes",
				Author:         "Arthur Conan Doyle",
				Unmatched:      true,
				LastActivityAt: 1709100000000,
			},
			metadata: nil,
		},
	}
}
