package gorobinson_test

import (
	"fmt"
	"testing"

	"github.com/jadoint/gorobinson"
	"github.com/jadoint/gorobinson/storage"
)

// benchLexer is a Lexer that returns a prebuilt token map whatever the text.
type benchLexer struct {
	tokens map[string]int
}

func (l *benchLexer) GetTokens(text string) (map[string]int, error) {
	return l.tokens, nil
}

// benchStore is an in-memory Storage holding every token of the text.
type benchStore struct {
	counts    map[string]storage.TokenCount
	internals storage.Internals
}

func (s *benchStore) GetInternals() (storage.Internals, error) {
	return s.internals, nil
}

func (s *benchStore) Get(tokens []string, deg storage.Degenerator) (*storage.TokenData, error) {
	found := make(map[string]storage.TokenCount, len(tokens))
	for _, tok := range tokens {
		if count, ok := s.counts[tok]; ok {
			found[tok] = count
		}
	}
	return &storage.TokenData{
		Tokens:      found,
		Degenerates: make(map[string]map[string]storage.TokenCount),
	}, nil
}

func (s *benchStore) ProcessText(tokens map[string]int, cat storage.Category, act storage.Action) error {
	return nil
}

// BenchmarkClassify times Classify end to end at text sizes up to the
// measured maximum. It uses exported API only, so the same file runs against
// v0.3.0 for a before/after comparison.
func BenchmarkClassify(b *testing.B) {
	// 42660 is the most distinct tokens of any item in the dev spam corpus
	// (asianfanfics/docs/gorobinson-top-k, Phase 1 measurement).
	for _, n := range []int{1000, 5000, 20000, 42660} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			tokens := make(map[string]int, n)
			counts := make(map[string]storage.TokenCount, n)
			for i := 0; i < n; i++ {
				tok := fmt.Sprintf("tok%06d", i)
				tokens[tok] = 1
				// Only 77 distinct count pairs, so most tokens tie on
				// importance with another token.
				counts[tok] = storage.TokenCount{
					CountHam:  int64(i%7 + 1),
					CountSpam: int64(i%11 + 1),
				}
			}

			lexer := &benchLexer{tokens: tokens}
			store := &benchStore{
				counts: counts,
				internals: storage.Internals{
					TextsHam:  1000,
					TextsSpam: 1000,
				},
			}
			c := gorobinson.NewWithComponents(store, lexer, gorobinson.NewStandardDegenerator())

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.Classify("x"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
