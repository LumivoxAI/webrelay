// Package pagination splits content without breaking UTF-8 sequences.
package pagination

import "errors"

var (
	// ErrInvalidOffset identifies a negative offset.
	ErrInvalidOffset = errors.New("offset must not be negative")
	// ErrInvalidLimit identifies a non-positive chunk limit.
	ErrInvalidLimit = errors.New("limit must be positive")
	// ErrOutOfRange identifies an offset past the end of content.
	ErrOutOfRange = errors.New("offset exceeds content length")
)

// Page is a Unicode code-point page of a document.
type Page struct {
	Content       string
	Offset        int
	ReturnedChars int
	TotalChars    int
	Truncated     bool
	NextOffset    *int
}

// Slice returns a page bounded by Unicode code points instead of UTF-8 bytes.
func Slice(content string, offset, limit int) (Page, error) {
	if offset < 0 {
		return Page{}, ErrInvalidOffset
	}
	if limit <= 0 {
		return Page{}, ErrInvalidLimit
	}

	characters := []rune(content)
	totalChars := len(characters)
	if offset > totalChars {
		return Page{}, ErrOutOfRange
	}
	end := min(offset+limit, totalChars)
	page := Page{
		Content:       string(characters[offset:end]),
		Offset:        offset,
		ReturnedChars: end - offset,
		TotalChars:    totalChars,
		Truncated:     end < totalChars,
	}
	if page.Truncated {
		nextOffset := end
		page.NextOffset = &nextOffset
	}
	return page, nil
}
