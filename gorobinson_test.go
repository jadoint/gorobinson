package gorobinson_test

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/jadoint/gorobinson"
	"github.com/jadoint/gorobinson/storage"
)

// memStorage is an in-memory Storage implementation used exclusively in tests.
// It is not part of the public API.
type memStorage struct {
	tokens    map[string]storage.TokenCount
	internals storage.Internals
}

func newMemStorage() *memStorage {
	return &memStorage{tokens: make(map[string]storage.TokenCount)}
}

func (m *memStorage) GetInternals() (storage.Internals, error) {
	return m.internals, nil
}

func (m *memStorage) Get(words []string, deg storage.Degenerator) (*storage.TokenData, error) {
	found := make(map[string]storage.TokenCount)
	missing := make([]string, 0)

	for _, w := range words {
		if tc, ok := m.tokens[w]; ok {
			found[w] = tc
		} else {
			missing = append(missing, w)
		}
	}

	degenerates := make(map[string]map[string]storage.TokenCount)
	if len(missing) > 0 && deg != nil {
		degMap := deg.Degenerate(missing)
		for original, forms := range degMap {
			degFound := make(map[string]storage.TokenCount)
			for _, form := range forms {
				if tc, ok := m.tokens[form]; ok {
					degFound[form] = tc
				}
			}
			if len(degFound) > 0 {
				degenerates[original] = degFound
			}
		}
	}

	return &storage.TokenData{Tokens: found, Degenerates: degenerates}, nil
}

func (m *memStorage) ProcessText(tokens map[string]int, cat storage.Category, act storage.Action) error {
	isSpam := cat == storage.Spam
	isLearn := act == storage.Learn

	for word, count := range tokens {
		tc := m.tokens[word]
		delta := int64(count)
		if !isLearn {
			delta = -delta
		}
		if isSpam {
			tc.CountSpam += delta
			if tc.CountSpam < 0 {
				tc.CountSpam = 0
			}
		} else {
			tc.CountHam += delta
			if tc.CountHam < 0 {
				tc.CountHam = 0
			}
		}
		m.tokens[word] = tc
	}

	delta := int64(1)
	if !isLearn {
		delta = -1
	}
	if isSpam {
		m.internals.TextsSpam += delta
		if m.internals.TextsSpam < 0 {
			m.internals.TextsSpam = 0
		}
	} else {
		m.internals.TextsHam += delta
		if m.internals.TextsHam < 0 {
			m.internals.TextsHam = 0
		}
	}
	return nil
}

// -----------------------------------------------------------------------
// Classifier tests
// -----------------------------------------------------------------------

func TestClassifyEmptyText(t *testing.T) {
	c := gorobinson.New(newMemStorage())
	_, err := c.Classify("")
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
}

func TestClassifyNoDataReturnsHalf(t *testing.T) {
	c := gorobinson.New(newMemStorage())
	score, err := c.Classify("hello world this is a test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.5 {
		t.Fatalf("expected 0.5, got %v", score)
	}
}

func TestClassifyZeroCountTokenReturnsFiniteNeutralScore(t *testing.T) {
	store := newMemStorage()
	store.tokens["poison"] = storage.TokenCount{}
	c := gorobinson.New(store)

	score, err := c.Classify("poison")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.IsNaN(score) || math.IsInf(score, 0) {
		t.Fatalf("expected finite score, got %v", score)
	}
	if score != 0.5 {
		t.Fatalf("expected neutral score for zero-count token, got %v", score)
	}
}

func TestLearnAndClassifySpam(t *testing.T) {
	store := newMemStorage()
	c := gorobinson.New(store)

	spamText := "buy cheap viagra now discount pills"
	hamText := "hello friend how are you doing today"

	for i := 0; i < 10; i++ {
		if err := c.Learn(spamText, gorobinson.Spam); err != nil {
			t.Fatalf("Learn spam: %v", err)
		}
		if err := c.Learn(hamText, gorobinson.Ham); err != nil {
			t.Fatalf("Learn ham: %v", err)
		}
	}

	spamScore, err := c.Classify(spamText)
	if err != nil {
		t.Fatalf("Classify spam: %v", err)
	}
	hamScore, err := c.Classify(hamText)
	if err != nil {
		t.Fatalf("Classify ham: %v", err)
	}

	if spamScore <= 0.5 {
		t.Errorf("expected spam score > 0.5, got %v", spamScore)
	}
	if hamScore >= 0.5 {
		t.Errorf("expected ham score < 0.5, got %v", hamScore)
	}
}

func TestUnlearn(t *testing.T) {
	store := newMemStorage()
	c := gorobinson.New(store)

	spamText := "win a free prize click here now"

	for i := 0; i < 5; i++ {
		if err := c.Learn(spamText, gorobinson.Spam); err != nil {
			t.Fatalf("Learn: %v", err)
		}
	}

	scoreBefore, err := c.Classify(spamText)
	if err != nil {
		t.Fatalf("Classify before unlearn: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := c.Unlearn(spamText, gorobinson.Spam); err != nil {
			t.Fatalf("Unlearn: %v", err)
		}
	}

	scoreAfter, err := c.Classify(spamText)
	if err != nil {
		t.Fatalf("Classify after unlearn: %v", err)
	}

	if math.Abs(scoreAfter-0.5) >= math.Abs(scoreBefore-0.5) {
		t.Errorf("expected score closer to 0.5 after unlearn; before=%v after=%v",
			scoreBefore, scoreAfter)
	}
}

func TestLearnEmptyText(t *testing.T) {
	c := gorobinson.New(newMemStorage())
	err := c.Learn("", gorobinson.Spam)
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestLearnInvalidCategory(t *testing.T) {
	c := gorobinson.New(newMemStorage())
	err := c.Learn("some text here", "unknown")
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
}

func TestNewUsesSpamTokensLexerByDefault(t *testing.T) {
	store := newMemStorage()
	c := gorobinson.New(store)

	raw := `<a href="https://promo.example/buy">FREE</a> 안녕 친구`
	if err := c.Learn(raw, gorobinson.Spam); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	wantCounts := map[string]int64{
		"tag_a":         2,
		"promo.example": 1,
		"promo":         1,
		"example":       1,
		"buy":           1,
		"free":          1,
		"script_hangul": 1,
	}
	for want, wantCount := range wantCounts {
		if got := store.tokens[want].CountSpam; got != wantCount {
			t.Fatalf("token %q CountSpam = %d, want %d; tokens=%v", want, got, wantCount, store.tokens)
		}
	}
	for _, legacy := range []string{"FREE", `<a...>`, "안녕"} {
		if _, ok := store.tokens[legacy]; ok {
			t.Fatalf("legacy/default lexer token %q should not be present; tokens=%v", legacy, store.tokens)
		}
	}
}

func TestUnlearnEmptyText(t *testing.T) {
	c := gorobinson.New(newMemStorage())
	err := c.Unlearn("", gorobinson.Ham)
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

// -----------------------------------------------------------------------
// Lexer tests
// -----------------------------------------------------------------------

func TestLexerEmptyText(t *testing.T) {
	l := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	_, err := l.GetTokens("")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestLexerBasicTokens(t *testing.T) {
	l := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	tokens, err := l.GetTokens("Hello world foo bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, word := range []string{"Hello", "world", "foo", "bar"} {
		if _, ok := tokens[word]; !ok {
			t.Errorf("expected token %q to be present", word)
		}
	}
}

func TestLexerMinSizeFilter(t *testing.T) {
	l := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	tokens, err := l.GetTokens("hi a to hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, short := range []string{"hi", "a", "to"} {
		if _, ok := tokens[short]; ok {
			t.Errorf("token %q should have been filtered (too short)", short)
		}
	}
	if _, ok := tokens["hello"]; !ok {
		t.Error("expected token \"hello\" to be present")
	}
}

func TestLexerNoTokensFallback(t *testing.T) {
	l := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	tokens, err := l.GetTokens("a b c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := tokens[gorobinson.NoTokensKey]; !ok {
		t.Errorf("expected %q fallback token", gorobinson.NoTokensKey)
	}
}

func TestLexerURIExtraction(t *testing.T) {
	l := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	tokens, err := l.GetTokens("visit example.com for details")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := tokens["example.com"]; !ok {
		t.Error("expected URI token \"example.com\" to be present")
	}
}

func TestLexerTokenCounting(t *testing.T) {
	l := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	tokens, err := l.GetTokens("spam spam spam")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens["spam"] < 2 {
		t.Errorf("expected \"spam\" to appear multiple times, got %d", tokens["spam"])
	}
}

// -----------------------------------------------------------------------
// Degenerator tests
// -----------------------------------------------------------------------

func TestDegeneratorBasic(t *testing.T) {
	d := gorobinson.NewStandardDegenerator()
	result := d.Degenerate([]string{"Hello"})
	forms := result["Hello"]
	if len(forms) == 0 {
		t.Fatal("expected at least one degenerate form")
	}
	found := false
	for _, f := range forms {
		if f == "hello" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected lowercase form \"hello\" in degenerates, got %v", forms)
	}
}

func TestDegeneratorTrailingPunctuation(t *testing.T) {
	d := gorobinson.NewStandardDegenerator()
	result := d.Degenerate([]string{"Hello!!"})
	forms := result["Hello!!"]
	wantStripped := false
	for _, f := range forms {
		if f == "Hello" || f == "hello" {
			wantStripped = true
			break
		}
	}
	if !wantStripped {
		t.Errorf("expected punctuation-stripped form in degenerates, got %v", forms)
	}
}

func TestDegeneratorTrailingDots(t *testing.T) {
	d := gorobinson.NewStandardDegenerator()
	result := d.Degenerate([]string{"Hello..."})
	forms := result["Hello..."]
	found := false
	for _, f := range forms {
		if f == "Hello" || f == "hello" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected dot-stripped form in degenerates, got %v", forms)
	}
}

func TestDegeneratorCaching(t *testing.T) {
	d := gorobinson.NewStandardDegenerator()
	first := d.Degenerate([]string{"World"})["World"]
	second := d.Degenerate([]string{"World"})["World"]
	if len(first) != len(second) {
		t.Errorf("cached result differs from original: %v vs %v", first, second)
	}
}

func TestDegeneratorConcurrentUse(t *testing.T) {
	d := gorobinson.NewStandardDegenerator()

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				word := fmt.Sprintf("Concurrent-%d-%d!!", id, j)
				result := d.Degenerate([]string{word})
				if len(result[word]) == 0 {
					t.Errorf("expected degenerates for %q", word)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestDegeneratorOriginalExcluded(t *testing.T) {
	d := gorobinson.NewStandardDegenerator()
	word := "Testing"
	forms := d.Degenerate([]string{word})[word]
	for _, f := range forms {
		if f == word {
			t.Errorf("original word %q should not appear in its own degenerate list", word)
		}
	}
}

// -----------------------------------------------------------------------
// Options tests
// -----------------------------------------------------------------------

func TestWithOptions(t *testing.T) {
	store := newMemStorage()
	c := gorobinson.New(store,
		gorobinson.WithUseRelevant(5),
		gorobinson.WithMinDev(0.1),
		gorobinson.WithRobS(0.5),
		gorobinson.WithRobX(0.5),
	)
	score, err := c.Classify("hello world testing options")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = score
}

func TestNewWithComponents(t *testing.T) {
	store := newMemStorage()
	lexer := gorobinson.NewStandardLexer(gorobinson.StandardLexerConfig())
	deg := gorobinson.NewStandardDegenerator()
	c := gorobinson.NewWithComponents(store, lexer, deg)
	score, err := c.Classify("hello world testing custom components")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = score
}
