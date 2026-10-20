package gorobinson

import "strings"

// Degenerator produces alternative forms ("degenerates") of a word so that
// the classifier can find a probability even for tokens not seen during
// training.
type Degenerator interface {
	// Degenerate returns a map from each input word to a slice of alternative
	// forms to try when the exact word is absent from the database.
	Degenerate(words []string) map[string][]string
}

// StandardDegenerator is the default Degenerator implementation.
// It is safe for concurrent use: it holds no state, and works out each word's
// forms afresh on every call.
type StandardDegenerator struct{}

// NewStandardDegenerator constructs a StandardDegenerator.
func NewStandardDegenerator() *StandardDegenerator {
	return &StandardDegenerator{}
}

// Degenerate implements Degenerator.
func (d *StandardDegenerator) Degenerate(words []string) map[string][]string {
	result := make(map[string][]string, len(words))
	for _, word := range words {
		result[word] = degenerateWord(word)
	}
	return result
}

// degenerateWord returns the degenerate forms of word: its case forms, then
// each candidate with its trailing run of '!' and '?' collapsed to the run's
// last byte (for a run of two or more) and stripped, then with its trailing
// dots stripped one at a time.
func degenerateWord(word string) []string {
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
		if n := trailingQB(alt); n > 0 {
			if n >= 2 {
				tmp := alt[:len(alt)-n] + alt[len(alt)-1:]
				expanded = appendIfAbsent(expanded, word, tmp)
			}
			tmp := alt[:len(alt)-n]
			expanded = appendIfAbsent(expanded, word, tmp)
		}

		cur := alt
		for strings.HasSuffix(cur, ".") {
			cur = cur[:len(cur)-1]
			expanded = appendIfAbsent(expanded, word, cur)
		}
	}

	return uniqueExcluding(word, expanded)
}

// trailingQB returns the number of trailing bytes of s that are '!' or '?',
// counted from the end up to the first other byte. Both are ASCII, and no
// byte of a UTF-8 multi-byte sequence is below 0x80, so byte checks give the
// same answer as a regular expression for every string, valid UTF-8 or not.
func trailingQB(s string) int {
	n := 0
	for n < len(s) {
		if c := s[len(s)-1-n]; c != '!' && c != '?' {
			break
		}
		n++
	}
	return n
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
