package pagination

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type PaginationSuite struct {
	suite.Suite
}

func (s *PaginationSuite) TestSliceUsesUnicodeCodePoints() {
	page, err := Slice("Go, мир! 👋", 4, 5)

	s.Require().NoError(err)
	s.Equal("мир! ", page.Content)
	s.Equal(10, page.TotalChars)
	s.Equal(5, page.ReturnedChars)
	s.True(page.Truncated)
	s.Require().NotNil(page.NextOffset)
	s.Equal(9, *page.NextOffset)
}

func (s *PaginationSuite) TestSliceHandlesDocumentEnd() {
	page, err := Slice("тест", 4, 10)

	s.Require().NoError(err)
	s.Empty(page.Content)
	s.Equal(0, page.ReturnedChars)
	s.Equal(4, page.TotalChars)
	s.False(page.Truncated)
	s.Nil(page.NextOffset)
}

func (s *PaginationSuite) TestSliceRejectsInvalidRanges() {
	for name, testCase := range map[string]struct {
		offset int
		limit  int
		err    error
	}{
		"negative offset": {offset: -1, limit: 1, err: ErrInvalidOffset},
		"zero limit":      {offset: 0, limit: 0, err: ErrInvalidLimit},
		"past end":        {offset: 5, limit: 1, err: ErrOutOfRange},
	} {
		s.Run(name, func() {
			_, err := Slice("тест", testCase.offset, testCase.limit)

			s.ErrorIs(err, testCase.err)
		})
	}
}

func TestPaginationSuite(t *testing.T) {
	suite.Run(t, new(PaginationSuite))
}
