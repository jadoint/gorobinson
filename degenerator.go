package gorobinson

import (
	"regexp"
	"strings"
)

// Degenerator produces alternative forms ("degenerates") of a word so that
// the classifier can find a probability even for tokens not seen during
// training.
type Degenerator interface {
	// Degenerate returns a map from each input word to a slice of alternative
	// forms to try when the exact word is absent from the database.
	Degenerate(words []string) map[string][]string
}

// Compiled regular expressions for the standard degenerator.
var (
	reTrailPunctDeg = regexp.MustCompile(`[!?]+$`)
	reMultiPunctDeg = regexp.MustCompile(`[!?]{2,}$`)
	reSinglePunct   = regexp.MustCompile(`([!?])+$`)
	reTrailDotDeg   = regexp.MustCompile(`\.$`)
)

// StandardDegenerator is the default Degenerator implementation.
// It is safe for concurrent read-only use after construction. The cache is
// populated lazily and is not concurrency-safe for writes; if concurrent
// learning is required, callers should construct a new StandardDegenerator per
// goroutine or guard access with a mutex.
type StandardDegenerator struct {
	cache map[string][]string
}

// NewStandardDegenerator constructs a StandardDegenerator with an empty cache.
func NewStandardDegenerator() *StandardDegenerator {
	return &StandardDegenerator{
		cache: make(map[string][]string),
	}
}

// Degenerate implements Degenerator.
func (d *StandardDegenerator) Degenerate(words []string) map[string][]string {
	result := make(map[string][]string, len(words))
	for _, word := range words {
		result[word] = d.degenerateWord(word)
	}
	return result
}

// degenerateWord returns the cached degenerate list for word, computing and
// caching it on the first call.
func (d *StandardDegenerator) degenerateWord(word string) []string {
	if cached, ok := d.cache[word]; ok {
		return cached
	}

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
		if reTrailPunctDeg.MatchString(alt) {
			if reMultiPunctDeg.MatchString(alt) {
				tmp := reSinglePunct.ReplaceAllString(alt, "$1")
				expanded = appendIfAbsent(expanded, word, tmp)
			}
			tmp := reTrailPunctDeg.ReplaceAllString(alt, "")
			expanded = appendIfAbsent(expanded, word, tmp)
		}

		cur := alt
		for reTrailDotDeg.MatchString(cur) {
			cur = cur[:len(cur)-1]
			expanded = appendIfAbsent(expanded, word, cur)
		}
	}

	degens := uniqueExcluding(word, expanded)
	d.cache[word] = degens
	return degens
}

// uniqueExcluding returns a new slice containing the elements of list that are
// not equal to exclude, preserving order and removing duplicates.
func uniqueExcluding(exclude string, list []string) []string {
	seen := make(map[string]bool, len(list))
	result := make([]string, 0, len(list))
	for _, s := range list {
		if s != exclude && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// appendIfAbsent appends s to list only when s differs from exclude and is not
// already present in list.
func appendIfAbsent(list []string, exclude, s string) []string {
	if s == exclude {
		return list
	}
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
