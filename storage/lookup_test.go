package storage

import (
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

// recordingFetch implements a fake fetchFunc over an in-memory table.
// It records each call's word slice (copying it) and can return an error
// on a specific 1-based call index.
type recordingFetch struct {
	table     map[string]TokenCount
	calls     [][]string
	errOnCall int
	err       error
}

func newRecordingFetch(table map[string]TokenCount) *recordingFetch {
	return &recordingFetch{
		table: table,
	}
}

func (rf *recordingFetch) fetch(words []string) (map[string]TokenCount, error) {
	cp := make([]string, len(words))
	copy(cp, words)
	rf.calls = append(rf.calls, cp)

	if rf.errOnCall > 0 && len(rf.calls) == rf.errOnCall {
		return nil, rf.err
	}

	result := make(map[string]TokenCount)
	for _, w := range words {
		if tc, ok := rf.table[w]; ok {
			result[w] = tc
		}
	}
	return result, nil
}

// degFunc is a function adapter implementing Degenerator.
type degFunc func([]string) map[string][]string

func (f degFunc) Degenerate(words []string) map[string][]string {
	if f == nil {
		return nil
	}
	return f(words)
}

// recordingDegenerator wraps degFunc and counts its calls while recording args.
type recordingDegenerator struct {
	fn    degFunc
	calls int
	args  [][]string
}

func newRecordingDegenerator(fn degFunc) *recordingDegenerator {
	return &recordingDegenerator{fn: fn}
}

func (d *recordingDegenerator) Degenerate(words []string) map[string][]string {
	d.calls++
	cp := make([]string, len(words))
	copy(cp, words)
	d.args = append(d.args, cp)
	if d.fn != nil {
		return d.fn(words)
	}
	return nil
}

func assertFetchInvariants(t *testing.T, calls [][]string) {
	t.Helper()
	seen := make(map[string]struct{})
	for i, call := range calls {
		if len(call) == 0 {
			t.Errorf("call %d: fetch called with 0 words", i)
		}
		if len(call) > lookupChunkSize {
			t.Errorf("call %d: fetch called with %d words, exceeds max %d", i, len(call), lookupChunkSize)
		}
		for _, w := range call {
			if _, dup := seen[w]; dup {
				t.Errorf("word %q fetched more than once across calls", w)
			}
			seen[w] = struct{}{}
		}
	}
}

func TestFetchChunkedSizes(t *testing.T) {
	sizes := []int{0, 1, 999, 1000, 1001, 2500, 70000}
	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			words := make([]string, n)
			table := make(map[string]TokenCount)
			expectedFound := make(map[string]TokenCount)
			for i := 0; i < n; i++ {
				w := fmt.Sprintf("w%06d", i)
				words[i] = w
				if i%3 == 0 {
					tc := TokenCount{CountHam: int64(i + 1), CountSpam: int64(i*2 + 1)}
					table[w] = tc
					expectedFound[w] = tc
				}
			}

			rf := newRecordingFetch(table)
			res, err := fetchChunked(words, rf.fetch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			expectedCalls := (n + lookupChunkSize - 1) / lookupChunkSize
			if len(rf.calls) != expectedCalls {
				t.Fatalf("call count: got %d, want ceil(%d/%d) = %d", len(rf.calls), n, lookupChunkSize, expectedCalls)
			}

			if n == 0 {
				if res == nil {
					t.Fatalf("expected non-nil result map for n=0")
				}
				if len(res) != 0 {
					t.Fatalf("expected empty result map for n=0, got %d entries", len(res))
				}
				if len(rf.calls) != 0 {
					t.Fatalf("expected zero calls for n=0, got %d", len(rf.calls))
				}
				return
			}

			assertFetchInvariants(t, rf.calls)

			var concatenated []string
			for _, call := range rf.calls {
				concatenated = append(concatenated, call...)
			}
			if !slices.Equal(concatenated, words) {
				t.Fatalf("calls' words concatenated do not equal input words in order")
			}

			if !reflect.DeepEqual(res, expectedFound) {
				t.Fatalf("result map does not match expected subset: got %v, want %v", res, expectedFound)
			}
		})
	}
}

func TestGetTokenDataChunkCounts(t *testing.T) {
	sizes := []int{0, 1, 999, 1000, 1001, 2500, 70000}
	for _, n := range sizes {
		words := make([]string, n)
		for i := 0; i < n; i++ {
			words[i] = fmt.Sprintf("w%06d", i)
		}

		t.Run(fmt.Sprintf("deg_nil/n=%d", n), func(t *testing.T) {
			rf := newRecordingFetch(nil)
			data, err := getTokenData(words, nil, rf.fetch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if data == nil {
				t.Fatalf("expected non-nil TokenData")
			}
			expectedCalls := (n + lookupChunkSize - 1) / lookupChunkSize
			if len(rf.calls) != expectedCalls {
				t.Fatalf("total calls: got %d, want %d", len(rf.calls), expectedCalls)
			}
			if n == 0 {
				if data.Tokens == nil || len(data.Tokens) != 0 {
					t.Fatalf("expected non-nil empty Tokens map, got: %v", data.Tokens)
				}
				if data.Degenerates == nil || len(data.Degenerates) != 0 {
					t.Fatalf("expected non-nil empty Degenerates map, got: %v", data.Degenerates)
				}
				return
			}
			assertFetchInvariants(t, rf.calls)
			var tokenPassWords []string
			for _, call := range rf.calls {
				tokenPassWords = append(tokenPassWords, call...)
			}
			if !slices.Equal(tokenPassWords, words) {
				t.Fatalf("token-pass calls concatenated do not equal input tokens")
			}
		})

		t.Run(fmt.Sprintf("with_deg/n=%d", n), func(t *testing.T) {
			rf := newRecordingFetch(nil)
			deg := newRecordingDegenerator(func(missing []string) map[string][]string {
				degMap := make(map[string][]string, len(missing))
				for _, m := range missing {
					degMap[m] = []string{"form_" + m}
				}
				return degMap
			})

			data, err := getTokenData(words, deg, rf.fetch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if data == nil {
				t.Fatalf("expected non-nil TokenData")
			}

			if n == 0 {
				if len(rf.calls) != 0 {
					t.Fatalf("expected 0 calls for empty tokens, got %d", len(rf.calls))
				}
				if deg.calls != 0 {
					t.Fatalf("degenerator should not be called for empty tokens, got %d calls", deg.calls)
				}
				if data.Tokens == nil || len(data.Tokens) != 0 {
					t.Fatalf("expected non-nil empty Tokens map, got: %v", data.Tokens)
				}
				if data.Degenerates == nil || len(data.Degenerates) != 0 {
					t.Fatalf("expected non-nil empty Degenerates map, got: %v", data.Degenerates)
				}
				return
			}

			if deg.calls != 1 {
				t.Fatalf("expected exactly 1 degenerator call, got %d", deg.calls)
			}

			tokenChunks := (n + lookupChunkSize - 1) / lookupChunkSize
			formChunks := (n + lookupChunkSize - 1) / lookupChunkSize
			expectedTotalCalls := tokenChunks + formChunks
			if len(rf.calls) != expectedTotalCalls {
				t.Fatalf("total calls: got %d, want %d (token=%d, form=%d)", len(rf.calls), expectedTotalCalls, tokenChunks, formChunks)
			}

			assertFetchInvariants(t, rf.calls)

			var tokenPassWords []string
			for _, call := range rf.calls[:tokenChunks] {
				tokenPassWords = append(tokenPassWords, call...)
			}
			if !slices.Equal(tokenPassWords, words) {
				t.Fatalf("first ceil(n/1000) calls concatenated do not equal input tokens")
			}

			var formPassWords []string
			for _, call := range rf.calls[tokenChunks:] {
				formPassWords = append(formPassWords, call...)
			}
			expectedForms := make([]string, n)
			for i, w := range words {
				expectedForms[i] = "form_" + w
			}
			if !slices.Equal(formPassWords, expectedForms) {
				t.Fatalf("form-pass calls concatenated do not equal expected forms")
			}
		})
	}
}

func TestGetTokenDataOverlappingForms(t *testing.T) {
	tokens := []string{"tok_found", "tok_miss1", "tok_miss2"}
	table := map[string]TokenCount{
		"tok_found":   {CountHam: 10, CountSpam: 20},
		"shared_form": {CountHam: 30, CountSpam: 40},
		"only_form":   {CountHam: 50, CountSpam: 60},
	}

	deg := newRecordingDegenerator(func(missing []string) map[string][]string {
		return map[string][]string{
			"tok_miss1": {"shared_form", "tok_found", "tok_miss2", "only_form"},
			"tok_miss2": {"shared_form", "unfound_form"},
		}
	})

	rf := newRecordingFetch(table)
	data, err := getTokenData(tokens, deg, rf.fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert: Degenerate called once with exactly missing in input order
	if deg.calls != 1 {
		t.Fatalf("expected Degenerate called once, got %d", deg.calls)
	}
	expectedMissing := []string{"tok_miss1", "tok_miss2"}
	if !slices.Equal(deg.args[0], expectedMissing) {
		t.Fatalf("Degenerate argument: got %v, want %v", deg.args[0], expectedMissing)
	}

	// Assert: form-pass calls concatenate to the exact expected toFetch list in INV-LK-4 order
	expectedToFetch := []string{"shared_form", "only_form", "unfound_form"}
	if len(rf.calls) < 2 {
		t.Fatalf("expected at least 2 fetch calls (token pass + form pass), got %d", len(rf.calls))
	}
	var formPassWords []string
	for _, call := range rf.calls[1:] {
		formPassWords = append(formPassWords, call...)
	}
	if !slices.Equal(formPassWords, expectedToFetch) {
		t.Fatalf("form-pass calls: got %v, want %v", formPassWords, expectedToFetch)
	}

	// Assert: no word fetched twice
	assertFetchInvariants(t, rf.calls)

	// Assert: full TokenData equals a hand-written expected value via reflect.DeepEqual
	expectedData := &TokenData{
		Tokens: map[string]TokenCount{
			"tok_found": {CountHam: 10, CountSpam: 20},
		},
		Degenerates: map[string]map[string]TokenCount{
			"tok_miss1": {
				"shared_form": {CountHam: 30, CountSpam: 40},
				"tok_found":   {CountHam: 10, CountSpam: 20},
				"only_form":   {CountHam: 50, CountSpam: 60},
			},
			"tok_miss2": {
				"shared_form": {CountHam: 30, CountSpam: 40},
			},
		},
	}
	if !reflect.DeepEqual(data, expectedData) {
		t.Fatalf("TokenData mismatch:\ngot  %+v\nwant %+v", data, expectedData)
	}
}

func TestGetTokenDataNilDegenerator(t *testing.T) {
	tokens := []string{"found1", "miss1", "miss2"}
	table := map[string]TokenCount{
		"found1": {CountHam: 5, CountSpam: 10},
	}
	rf := newRecordingFetch(table)

	data, err := getTokenData(tokens, nil, rf.fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil {
		t.Fatalf("expected non-nil TokenData")
	}

	// Assert: one token pass only
	expectedCalls := (len(tokens) + lookupChunkSize - 1) / lookupChunkSize
	if len(rf.calls) != expectedCalls {
		t.Fatalf("expected %d fetch calls, got %d", expectedCalls, len(rf.calls))
	}
	assertFetchInvariants(t, rf.calls)
	var fetched []string
	for _, call := range rf.calls {
		fetched = append(fetched, call...)
	}
	if !slices.Equal(fetched, tokens) {
		t.Fatalf("fetched tokens do not match input tokens")
	}

	// Assert: Tokens map has found tokens
	expectedTokens := map[string]TokenCount{
		"found1": {CountHam: 5, CountSpam: 10},
	}
	if !reflect.DeepEqual(data.Tokens, expectedTokens) {
		t.Fatalf("Tokens mismatch: got %+v, want %+v", data.Tokens, expectedTokens)
	}

	// Assert: Degenerates non-nil and empty
	if data.Degenerates == nil {
		t.Fatalf("Degenerates map should be non-nil")
	}
	if len(data.Degenerates) != 0 {
		t.Fatalf("Degenerates map should be empty, got: %v", data.Degenerates)
	}
}

func TestGetTokenDataAllFound(t *testing.T) {
	tokens := []string{"t1", "t2", "t3"}
	table := map[string]TokenCount{
		"t1": {CountHam: 1, CountSpam: 2},
		"t2": {CountHam: 3, CountSpam: 4},
		"t3": {CountHam: 5, CountSpam: 6},
	}
	rf := newRecordingFetch(table)
	deg := newRecordingDegenerator(func(missing []string) map[string][]string {
		t.Fatalf("degenerator should never be called when all tokens are found")
		return nil
	})

	data, err := getTokenData(tokens, deg, rf.fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil {
		t.Fatalf("expected non-nil TokenData")
	}

	// Assert: degenerator never called
	if deg.calls != 0 {
		t.Fatalf("expected degenerator calls == 0, got %d", deg.calls)
	}

	// Assert: no form pass (only token pass calls)
	expectedCalls := (len(tokens) + lookupChunkSize - 1) / lookupChunkSize
	if len(rf.calls) != expectedCalls {
		t.Fatalf("expected %d fetch calls (no form pass), got %d", expectedCalls, len(rf.calls))
	}

	// Assert: Degenerates non-nil and empty
	if data.Degenerates == nil {
		t.Fatalf("expected non-nil Degenerates map")
	}
	if len(data.Degenerates) != 0 {
		t.Fatalf("expected empty Degenerates map, got: %v", data.Degenerates)
	}

	// Assert: Tokens contains all stored tokens
	if !reflect.DeepEqual(data.Tokens, table) {
		t.Fatalf("Tokens mismatch: got %+v, want %+v", data.Tokens, table)
	}
}

func TestGetTokenDataDegeneratorEdgeCases(t *testing.T) {
	t.Run("empty form list", func(t *testing.T) {
		tokens := []string{"miss1"}
		rf := newRecordingFetch(nil)
		deg := newRecordingDegenerator(func(missing []string) map[string][]string {
			return map[string][]string{
				"miss1": {},
			}
		})

		data, err := getTokenData(tokens, deg, rf.fetch)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if data == nil {
			t.Fatalf("expected non-nil TokenData")
		}

		if deg.calls != 1 {
			t.Fatalf("expected 1 degenerator call, got %d", deg.calls)
		}
		// Token pass made 1 call; form pass should make 0 calls because toFetch is empty
		if len(rf.calls) != 1 {
			t.Fatalf("expected 1 fetch call (no form-pass call), got %d", len(rf.calls))
		}
		if data.Degenerates == nil {
			t.Fatalf("expected non-nil Degenerates map")
		}
		if _, ok := data.Degenerates["miss1"]; ok {
			t.Fatalf("expected no entry for key with empty form list, got: %v", data.Degenerates["miss1"])
		}
	})

	t.Run("empty string form", func(t *testing.T) {
		tokens := []string{"miss1"}
		table := map[string]TokenCount{
			"": {CountHam: 42, CountSpam: 99},
		}
		rf := newRecordingFetch(table)
		deg := newRecordingDegenerator(func(missing []string) map[string][]string {
			return map[string][]string{
				"miss1": {""},
			}
		})

		data, err := getTokenData(tokens, deg, rf.fetch)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if data == nil {
			t.Fatalf("expected non-nil TokenData")
		}

		if len(rf.calls) != 2 {
			t.Fatalf("expected 2 fetch calls (token pass + form pass), got %d", len(rf.calls))
		}
		if !slices.Equal(rf.calls[1], []string{""}) {
			t.Fatalf("expected form pass to fetch empty string form, got: %v", rf.calls[1])
		}

		expectedDeg := map[string]TokenCount{
			"": {CountHam: 42, CountSpam: 99},
		}
		if !reflect.DeepEqual(data.Degenerates["miss1"], expectedDeg) {
			t.Fatalf("Degenerates[miss1] mismatch: got %+v, want %+v", data.Degenerates["miss1"], expectedDeg)
		}
	})

	t.Run("extra keys in sorted order", func(t *testing.T) {
		tokens := []string{"miss_b", "miss_a"}
		table := map[string]TokenCount{
			"f_b":   {CountHam: 1, CountSpam: 1},
			"f_a":   {CountHam: 2, CountSpam: 2},
			"f_app": {CountHam: 3, CountSpam: 3},
			"f_z":   {CountHam: 4, CountSpam: 4},
		}
		rf := newRecordingFetch(table)
		deg := newRecordingDegenerator(func(missing []string) map[string][]string {
			return map[string][]string{
				"miss_b": {"f_b"},
				"miss_a": {"f_a"},
				"zebra":  {"f_z"},
				"apple":  {"f_app"},
			}
		})

		data, err := getTokenData(tokens, deg, rf.fetch)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if data == nil {
			t.Fatalf("expected non-nil TokenData")
		}

		if len(rf.calls) != 2 {
			t.Fatalf("expected 2 fetch calls, got %d", len(rf.calls))
		}
		// In-missing keys order: miss_b ("f_b"), then miss_a ("f_a").
		// Extra keys sorted order: apple ("f_app"), then zebra ("f_z").
		expectedFormPass := []string{"f_b", "f_a", "f_app", "f_z"}
		if !slices.Equal(rf.calls[1], expectedFormPass) {
			t.Fatalf("form-pass order mismatch:\ngot  %v\nwant %v", rf.calls[1], expectedFormPass)
		}

		expectedDegenerates := map[string]map[string]TokenCount{
			"miss_b": {"f_b": {CountHam: 1, CountSpam: 1}},
			"miss_a": {"f_a": {CountHam: 2, CountSpam: 2}},
			"apple":  {"f_app": {CountHam: 3, CountSpam: 3}},
			"zebra":  {"f_z": {CountHam: 4, CountSpam: 4}},
		}
		if !reflect.DeepEqual(data.Degenerates, expectedDegenerates) {
			t.Fatalf("Degenerates mismatch:\ngot  %+v\nwant %+v", data.Degenerates, expectedDegenerates)
		}
	})
}

func TestGetTokenDataErrors(t *testing.T) {
	sentinel := errors.New("sentinel database fetch error")

	// 2500 tokens -> 3 chunks of tokens (1000, 1000, 500)
	// Degenerator yields 2500 forms -> 3 chunks of forms (1000, 1000, 500)
	const numTokens = 2500
	tokens := make([]string, numTokens)
	for i := 0; i < numTokens; i++ {
		tokens[i] = fmt.Sprintf("tok%04d", i)
	}

	testCases := []struct {
		name        string
		failCall    int
		isTokenPass bool
	}{
		{"token pass first call", 1, true},
		{"token pass middle call", 2, true},
		{"token pass last call", 3, true},
		{"form pass first call", 4, false},
		{"form pass middle call", 5, false},
		{"form pass last call", 6, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rf := newRecordingFetch(nil)
			rf.errOnCall = tc.failCall
			rf.err = sentinel

			deg := newRecordingDegenerator(func(missing []string) map[string][]string {
				degMap := make(map[string][]string, len(missing))
				for _, m := range missing {
					degMap[m] = []string{"form_" + m}
				}
				return degMap
			})

			data, err := getTokenData(tokens, deg, rf.fetch)
			if !errors.Is(err, sentinel) {
				t.Fatalf("expected errors.Is(err, sentinel), got: %v", err)
			}
			if data != nil {
				t.Fatalf("expected nil *TokenData on error, got: %+v", data)
			}
			if len(rf.calls) != tc.failCall {
				t.Fatalf("calls recorded: got %d, want failing call number %d (no later calls)", len(rf.calls), tc.failCall)
			}
			if tc.isTokenPass && deg.calls != 0 {
				t.Fatalf("Degenerate should not be called when token pass fails, got %d calls", deg.calls)
			}
			if !tc.isTokenPass && deg.calls != 1 {
				t.Fatalf("Degenerate should be called exactly once before form pass fails, got %d calls", deg.calls)
			}
		})
	}
}

// referenceGet is v0.3.1's Get algorithm (storage/mysql.go and
// storage/postgres.go at v0.3.1): one unchunked fetch of every token, then
// one fetch per Degenerate entry, keeping the non-empty results.
func referenceGet(tokens []string, deg Degenerator, fetch fetchFunc) (*TokenData, error) {
	if len(tokens) == 0 {
		return &TokenData{
			Tokens:      make(map[string]TokenCount),
			Degenerates: make(map[string]map[string]TokenCount),
		}, nil
	}

	found, err := fetch(tokens)
	if err != nil {
		return nil, err
	}

	missing := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, ok := found[t]; !ok {
			missing = append(missing, t)
		}
	}

	degenerates := make(map[string]map[string]TokenCount)
	if len(missing) > 0 && deg != nil {
		degMap := deg.Degenerate(missing)
		for original, forms := range degMap {
			degFound, err := fetch(forms)
			if err != nil {
				return nil, err
			}
			if len(degFound) > 0 {
				degenerates[original] = degFound
			}
		}
	}

	return &TokenData{
		Tokens:      found,
		Degenerates: degenerates,
	}, nil
}

// exactFetch is an unchunked, exact-match fetch over table, shaped like
// v0.3.1's fetchTokens: zero words returns an empty map.
func exactFetch(table map[string]TokenCount) fetchFunc {
	return func(words []string) (map[string]TokenCount, error) {
		result := make(map[string]TokenCount, len(words))
		for _, w := range words {
			if tc, ok := table[w]; ok {
				result[w] = tc
			}
		}
		return result, nil
	}
}

// refDegenerator is a test Degenerator built from a function of one word.
// extra, when set, adds keys Degenerate was not asked about.
type refDegenerator struct {
	forms func(word string) []string
	extra func(words []string) map[string][]string
}

func (d refDegenerator) Degenerate(words []string) map[string][]string {
	result := make(map[string][]string, len(words))
	for _, w := range words {
		result[w] = d.forms(w)
	}
	if d.extra != nil {
		maps.Copy(result, d.extra(words))
	}
	return result
}

// standardShapedForms mimics StandardDegenerator: case forms, then the case
// forms with trailing '!', '?' and '.' runs stripped, the word itself
// excluded, duplicates dropped.
func standardShapedForms(word string) []string {
	lower := strings.ToLower(word)
	upper := strings.ToUpper(word)
	title := upper
	if len(lower) > 1 {
		title = upper[:1] + lower[1:]
	}
	cands := []string{lower, upper, title}
	for _, c := range []string{lower, upper, title, word} {
		cands = append(cands, strings.TrimRight(c, "!?."), strings.TrimRight(c, "."))
	}
	var forms []string
	seen := map[string]bool{word: true}
	for _, c := range cands {
		if !seen[c] {
			seen[c] = true
			forms = append(forms, c)
		}
	}
	return forms
}

var refSuffixes = []string{"", "", "", "!", "?", "!!", ".", "..", "?!", "!?."}

func refWord(r *rand.Rand) string {
	const letters = "abAB"
	b := make([]byte, 1+r.IntN(6))
	for i := range b {
		b[i] = letters[r.IntN(len(letters))]
	}
	return string(b) + refSuffixes[r.IntN(len(refSuffixes))]
}

func TestGetTokenDataMatchesReference(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for trial := range 200 {
		table := make(map[string]TokenCount)
		for range r.IntN(5001) {
			table[refWord(r)] = TokenCount{CountHam: r.Int64N(100), CountSpam: r.Int64N(100)}
		}
		stored := make([]string, 0, len(table))
		for w := range table {
			stored = append(stored, w)
		}
		sort.Strings(stored)

		want := r.IntN(3001)
		tokens := make([]string, 0, want)
		used := make(map[string]bool, want)
		for tries := 0; len(tokens) < want && tries < want*20; tries++ {
			var w string
			if len(stored) > 0 && r.IntN(2) == 0 {
				w = stored[r.IntN(len(stored))]
			} else {
				w = refWord(r)
			}
			if !used[w] {
				used[w] = true
				tokens = append(tokens, w)
			}
		}

		var deg Degenerator
		var kind string
		switch trial % 3 {
		case 0:
			kind = "standard"
			deg = refDegenerator{forms: standardShapedForms}
		case 1:
			kind = "empty-key"
			deg = refDegenerator{forms: func(w string) []string {
				switch len(w) % 3 {
				case 0:
					return nil
				case 1:
					return []string{}
				default:
					return append([]string{""}, standardShapedForms(w)...)
				}
			}}
			table[""] = TokenCount{CountHam: 1, CountSpam: 2}
		case 2:
			kind = "extra-key"
			deg = refDegenerator{
				forms: standardShapedForms,
				extra: func(words []string) map[string][]string {
					extra := make(map[string][]string)
					for i, w := range words {
						if i%7 == 0 {
							extra["extra:"+w] = append([]string{w + "!"}, standardShapedForms(w+"?")...)
						}
					}
					if len(stored) > 0 {
						extra[stored[0]] = []string{strings.ToUpper(stored[0])}
					}
					return extra
				},
			}
		}

		fetch := exactFetch(table)
		got, err := getTokenData(tokens, deg, fetch)
		if err != nil {
			t.Fatalf("trial %d (%s): getTokenData: %v", trial, kind, err)
		}
		ref, err := referenceGet(tokens, deg, fetch)
		if err != nil {
			t.Fatalf("trial %d (%s): referenceGet: %v", trial, kind, err)
		}
		if !reflect.DeepEqual(got, ref) {
			t.Fatalf("trial %d (%s): %d tokens, %d stored: getTokenData differs from referenceGet\n got  %d tokens, %d degenerates\n want %d tokens, %d degenerates",
				trial, kind, len(tokens), len(table), len(got.Tokens), len(got.Degenerates), len(ref.Tokens), len(ref.Degenerates))
		}
	}
}
