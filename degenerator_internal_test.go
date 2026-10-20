package gorobinson

import (
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The four regular expressions v0.3.1's degenerator used, renamed ref*.
var (
	refTrailPunctDeg = regexp.MustCompile(`[!?]+$`)
	refMultiPunctDeg = regexp.MustCompile(`[!?]{2,}$`)
	refSinglePunct   = regexp.MustCompile(`([!?])+$`)
	refTrailDotDeg   = regexp.MustCompile(`\.$`)
)

// referenceDegenerateWord is v0.3.1's degenerateWord without the cache, the
// reference the rewrite must equal.
func referenceDegenerateWord(word string) []string {
	lower := strings.ToLower(word)
	upper := strings.ToUpper(word)

	// Title-case: first rune uppercased, rest lowercased.
	var first string
	if len(lower) > 0 {
		runes := []rune(lower)
		if len(upper) > 0 {
			first = string([]rune(upper)[:1]) + string(runes[1:])
		}
	}

	candidates := uniqueExcluding(word, []string{lower, upper, first})
	// Always add the original word at the end so storage can match it too.
	candidates = append(candidates, word)

	expanded := make([]string, len(candidates))
	copy(expanded, candidates)

	for _, alt := range candidates {
		if refTrailPunctDeg.MatchString(alt) {
			if refMultiPunctDeg.MatchString(alt) {
				tmp := refSinglePunct.ReplaceAllString(alt, "$1")
				expanded = appendIfAbsent(expanded, word, tmp)
			}
			tmp := refTrailPunctDeg.ReplaceAllString(alt, "")
			expanded = appendIfAbsent(expanded, word, tmp)
		}

		cur := alt
		for refTrailDotDeg.MatchString(cur) {
			cur = cur[:len(cur)-1]
			expanded = appendIfAbsent(expanded, word, cur)
		}
	}

	degens := uniqueExcluding(word, expanded)
	return degens
}

// degenerateTable is the fixed equivalence table and the fuzz seed corpus:
// every trailing-punctuation shape, multi-byte runes on either side of it,
// and invalid UTF-8.
var degenerateTable = []string{
	"", "!", "?", "!?", "a!?!", "Hello!!", "HELLO?!?", "x.", "x...", "x.!.",
	"...", "日本語!?", "ß!", "İstanbul?", "\xff!", "a\xff?!", "Word.!!", " !",
	"a b?",
}

// seededDegenerateWords returns n words of 0 to 12 pieces from a fixed
// alphabet, from a PCG seeded (3, 4), so every run checks the same words.
func seededDegenerateWords(n int) []string {
	r := rand.New(rand.NewPCG(3, 4))
	alphabet := []string{"a", "Z", "ß", "İ", "日", "!", "?", ".", " ", "\xff"}
	words := make([]string, n)
	for i := range words {
		var sb strings.Builder
		for range r.IntN(13) {
			sb.WriteString(alphabet[r.IntN(len(alphabet))])
		}
		words[i] = sb.String()
	}
	return words
}

// matchesReference reports whether got, the forms Degenerate returned for
// word, equal the reference's, order included, and reports a mismatch on t.
func matchesReference(t testing.TB, word string, got []string) bool {
	t.Helper()
	want := referenceDegenerateWord(word)
	if reflect.DeepEqual(got, want) {
		return true
	}
	t.Errorf("word %q: got %q, want %q", word, got, want)
	return false
}

func TestDegenerateMatchesReference(t *testing.T) {
	words := append(append([]string{}, degenerateTable...), seededDegenerateWords(100000)...)

	failures := 0
	check := func(word string, got []string) {
		t.Helper()
		if !matchesReference(t, word, got) {
			failures++
			if failures >= 10 {
				t.FailNow()
			}
		}
	}

	// One word per call, each on a fresh degenerator.
	for _, word := range words {
		check(word, NewStandardDegenerator().Degenerate([]string{word})[word])
	}

	// Every word in one call on one degenerator.
	results := NewStandardDegenerator().Degenerate(words)
	for _, word := range words {
		check(word, results[word])
	}
}

func FuzzDegenerateMatchesReference(f *testing.F) {
	for _, word := range degenerateTable {
		f.Add(word)
	}
	f.Fuzz(func(t *testing.T, word string) {
		matchesReference(t, word, NewStandardDegenerator().Degenerate([]string{word})[word])
	})
}
