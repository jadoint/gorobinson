package gorobinson

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/jadoint/gorobinson/storage"
)

// equalSlices treats nil and empty as equal.
func equalSlices(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertSlicesEqual(t *testing.T, got, want []string, desc string) {
	t.Helper()
	if !equalSlices(got, want) {
		t.Errorf("%s: got %v, want %v", desc, got, want)
	}
}

// refTopRelevant is topRelevant's reference: every qualifying token, fully
// sorted by (importance desc, token asc), cut to k.
func refTopRelevant(importances map[string]float64, k int, minDev float64) []string {
	if k <= 0 {
		return nil
	}
	var keys []string
	for key, val := range importances {
		if val > minDev {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Slice(keys, func(i, j int) bool {
		vi, vj := importances[keys[i]], importances[keys[j]]
		if vi != vj {
			return vi > vj
		}
		return strings.Compare(keys[i], keys[j]) < 0
	})
	if len(keys) > k {
		keys = keys[:k]
	}
	return keys
}

// TestTopRelevantTable pins topRelevant's order, tie-break and edge cases
// (INV-SEL-1, INV-SEL-2).
func TestTopRelevantTable(t *testing.T) {
	tests := []struct {
		name        string
		importances map[string]float64
		k           int
		minDev      float64
		expected    []string
	}{
		{
			name:        "empty map",
			importances: map[string]float64{},
			k:           15,
			minDev:      0.2,
			expected:    []string{},
		},
		{
			name:        "nil map",
			importances: nil,
			k:           15,
			minDev:      0.2,
			expected:    []string{},
		},
		{
			name:        "k 0 with qualifying tokens",
			importances: map[string]float64{"a": 0.3, "b": 0.4},
			k:           0,
			minDev:      0.2,
			expected:    []string{},
		},
		{
			name:        "k -1 with qualifying tokens",
			importances: map[string]float64{"a": 0.3, "b": 0.4},
			k:           -1,
			minDev:      0.2,
			expected:    []string{},
		},
		{
			name:        "fewer qualifying tokens than k",
			importances: map[string]float64{"a": 0.25, "b": 0.35, "c": 0.45},
			k:           15,
			minDev:      0.2,
			expected:    []string{"c", "b", "a"},
		},
		{
			name:        "all at or below minDev",
			importances: map[string]float64{"a": 0.2, "b": 0.1, "c": 0.0},
			k:           15,
			minDev:      0.2,
			expected:    []string{},
		},
		{
			name:        "exactly minDev excluded",
			importances: map[string]float64{"a": 0.2, "b": 0.3},
			k:           15,
			minDev:      0.2,
			expected:    []string{"b"},
		},
		{
			name:        "NaN excluded",
			importances: map[string]float64{"a": math.NaN(), "b": 0.3, "c": 0.25},
			k:           15,
			minDev:      0.2,
			expected:    []string{"b", "c"},
		},
		{
			name:        "ties across the k boundary",
			importances: map[string]float64{"d": 0.4, "c": 0.4, "b": 0.4, "a": 0.4, "z": 0.45},
			k:           3,
			minDev:      0.2,
			expected:    []string{"z", "a", "b"},
		},
		{
			name:        "ties inside the result",
			importances: map[string]float64{"m": 0.3, "b": 0.3, "x": 0.49, "k": 0.21},
			k:           15,
			minDev:      0.2,
			expected:    []string{"x", "b", "m", "k"},
		},
		{
			name:        "descending with no ties, k smaller than qualifying count",
			importances: map[string]float64{"a": 0.21, "b": 0.31, "c": 0.41, "d": 0.49},
			k:           2,
			minDev:      0.2,
			expected:    []string{"d", "c"},
		},
		{
			name: "Han/Kana bigrams against Latin tokens in byte order",
			importances: map[string]float64{
				"漢字":    0.25,
				"かな":    0.25,
				"zebra": 0.25,
				"Apple": 0.25,
				"apple": 0.25,
				"カナ":    0.25,
			},
			k:        15,
			minDev:   0.2,
			expected: []string{"Apple", "apple", "zebra", "かな", "カナ", "漢字"},
		},
		{
			name:        "k larger than the map with ties and below-threshold mixed",
			importances: map[string]float64{"b": 0.25, "a": 0.25, "c": 0.1, "d": 0.2},
			k:           10,
			minDev:      0.2,
			expected:    []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := topRelevant(tc.importances, tc.k, tc.minDev)
			assertSlicesEqual(t, got, tc.expected, tc.name)
		})
	}
}

// TestTopRelevantMatchesSortReference checks topRelevant against a full
// sort.Slice over the same comparator on random maps quantised to force ties
// (INV-SEL-1).
func TestTopRelevantMatchesSortReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	kChoices := []int{0, 1, 5, 15, 50}
	const minDev = 0.2

	for iter := 0; iter < 200; iter++ {
		n := rng.Intn(5001)
		importances := make(map[string]float64, n)
		for i := 0; i < n; i++ {
			importances[fmt.Sprintf("t%x", rng.Int63())] = float64(rng.Intn(11)) * 0.05
		}
		k := kChoices[rng.Intn(len(kChoices))]

		got := topRelevant(importances, k, minDev)
		want := refTopRelevant(importances, k, minDev)

		if !equalSlices(got, want) {
			gotTrunc := got
			if len(gotTrunc) > 20 {
				gotTrunc = gotTrunc[:20]
			}
			wantTrunc := want
			if len(wantTrunc) > 20 {
				wantTrunc = wantTrunc[:20]
			}
			t.Fatalf("iteration %d: len=%d, k=%d: got %v, want %v", iter, len(importances), k, gotTrunc, wantTrunc)
		}
	}
}

// TestTokenProbabilityDegenerateTieIsDeterministic pins the degenerate-form
// tie-break: of two forms at exactly the same |0.5 - p|, the one whose bytes
// sort first wins (INV-SEL-5). The tie is exact in floating point (INV-SEL-7).
func TestTokenProbabilityDegenerateTieIsDeterministic(t *testing.T) {
	c := New(nil, WithRobS(0))
	var textsHam, textsSpam int64 = 16, 16

	tokenData := &storage.TokenData{
		Tokens: map[string]storage.TokenCount{},
		Degenerates: map[string]map[string]storage.TokenCount{
			"Word!": {
				"word": {CountHam: 1, CountSpam: 3},
				"Word": {CountHam: 3, CountSpam: 1},
				"WORD": {CountHam: 2, CountSpam: 2},
			},
		},
	}

	pa := c.robinsonProbability(storage.TokenCount{CountHam: 1, CountSpam: 3}, textsHam, textsSpam)
	pb := c.robinsonProbability(storage.TokenCount{CountHam: 3, CountSpam: 1}, textsHam, textsSpam)
	if pa != 0.75 || pb != 0.25 || math.Abs(0.5-pa) != math.Abs(0.5-pb) {
		t.Fatalf("tie is not exact: pa=%v, pb=%v", pa, pb)
	}

	for i := 0; i < 100; i++ {
		got := c.tokenProbability("Word!", textsHam, textsSpam, tokenData)
		if got != 0.25 {
			t.Fatalf("call %d: got %v, want 0.25", i, got)
		}
	}
}
