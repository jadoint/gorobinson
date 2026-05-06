// Package gorobinson implements Gary Robinson's spam classification algorithm,
// a refinement of the Naive Bayesian approach.
//
// Robinson's algorithm handles rare tokens more robustly than standard Naive
// Bayes by weighting each token's probability against a prior using a strength
// parameter. The most significant tokens are combined via a Fisher-derived
// geometric mean to produce a final spam probability.
//
// Basic usage:
//
//	db, _ := sql.Open("postgres", dsn)
//	store := storage.NewPostgres(db, "spam_tokens")
//	c := gorobinson.New(store)
//	score, err := c.Classify("some text")
//	err = c.Learn("spam text", gorobinson.Spam)
//	err = c.Unlearn("spam text", gorobinson.Spam)
package gorobinson

import (
	"errors"
	"math"

	"github.com/jadoint/gorobinson/storage"
)

// Category indicates whether a text is spam or ham.
// It is an alias for storage.Category so callers need not import storage
// just to pass Spam or Ham.
type Category = storage.Category

const (
	// Spam marks a text as spam during training.
	Spam = storage.Spam
	// Ham marks a text as legitimate (not spam) during training.
	Ham = storage.Ham
)

// ErrTextMissing is returned when an empty string is passed to Classify, Learn, or Unlearn.
var ErrTextMissing = errors.New("gorobinson: text must not be empty")

// ErrCategoryInvalid is returned when a category other than Spam or Ham is provided.
var ErrCategoryInvalid = errors.New("gorobinson: category must be Spam or Ham")

// Config holds the tuning parameters for the classifier. The zero value is
// not useful; use DefaultConfig() to obtain sensible defaults.
type Config struct {
	// UseRelevant is the maximum number of tokens considered when computing
	// the spam probability (default 15).
	UseRelevant int

	// MinDev is the minimum deviation from 0.5 a token probability must have
	// before it is considered significant (default 0.2).
	MinDev float64

	// RobS is the strength of the Robinson formula's prior. A higher value
	// makes the classifier more conservative (default 0.3).
	RobS float64

	// RobX is the assumed probability for unseen tokens (default 0.5).
	RobX float64
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		UseRelevant: 15,
		MinDev:      0.2,
		RobS:        0.3,
		RobX:        0.5,
	}
}

// Option is a functional option for configuring a Classifier.
type Option func(*Config)

// WithUseRelevant sets the maximum number of relevant tokens used when
// computing the spam probability.
func WithUseRelevant(n int) Option {
	return func(c *Config) { c.UseRelevant = n }
}

// WithMinDev sets the minimum deviation from 0.5 required for a token to be
// considered significant.
func WithMinDev(d float64) Option {
	return func(c *Config) { c.MinDev = d }
}

// WithRobS sets the strength of the Robinson formula's prior.
func WithRobS(s float64) Option {
	return func(c *Config) { c.RobS = s }
}

// WithRobX sets the assumed probability for tokens that have never been seen.
func WithRobX(x float64) Option {
	return func(c *Config) { c.RobX = x }
}

// Classifier is the main spam-classification object. It is safe for concurrent
// read use (Classify). Concurrent calls to Learn or Unlearn are serialised by
// the underlying storage implementation.
type Classifier struct {
	config      Config
	store       storage.Storage
	lexer       Lexer
	degenerator Degenerator
}

// New creates a Classifier backed by the provided Storage implementation.
// Pass functional Option values to override defaults.
func New(store storage.Storage, opts ...Option) *Classifier {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Classifier{
		config:      cfg,
		store:       store,
		lexer:       NewStandardLexer(StandardLexerConfig()),
		degenerator: NewStandardDegenerator(),
	}
}

// NewWithComponents creates a Classifier with custom Lexer and Degenerator
// implementations in addition to the storage backend and config options.
func NewWithComponents(store storage.Storage, lexer Lexer, deg Degenerator, opts ...Option) *Classifier {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Classifier{
		config:      cfg,
		store:       store,
		lexer:       lexer,
		degenerator: deg,
	}
}

// Classify analyses text and returns a spam probability in the range [0, 1].
// A value close to 1 indicates spam; a value close to 0 indicates ham.
// 0.5 is returned when there is insufficient evidence to decide.
//
// The final probability is calculated by combining the most significant
// tokens (those whose probabilities deviate most from 0.5) using Gary
// Robinson's geometric mean combination method.
func (c *Classifier) Classify(text string) (float64, error) {
	if text == "" {
		return 0, ErrTextMissing
	}

	internals, err := c.store.GetInternals()
	if err != nil {
		return 0, err
	}

	tokens, err := c.lexer.GetTokens(text)
	if err != nil {
		return 0, err
	}

	tokenWords := make([]string, 0, len(tokens))
	for word := range tokens {
		tokenWords = append(tokenWords, word)
	}

	tokenData, err := c.store.Get(tokenWords, c.degenerator)
	if err != nil {
		return 0, err
	}

	ratings := make(map[string]float64, len(tokens))
	importances := make(map[string]float64, len(tokens))
	for word := range tokens {
		p := c.tokenProbability(word, internals.TextsHam, internals.TextsSpam, tokenData)
		ratings[word] = p
		importances[word] = math.Abs(0.5 - p)
	}

	sorted := sortedKeysByValueDesc(importances)

	relevant := make([]float64, 0, c.config.UseRelevant)
	for i := 0; i < c.config.UseRelevant && i < len(sorted); i++ {
		token := sorted[i]
		if math.Abs(0.5-ratings[token]) <= c.config.MinDev {
			break
		}
		count := tokens[token]
		for x := 0; x < count; x++ {
			relevant = append(relevant, ratings[token])
		}
	}

	if len(relevant) == 0 {
		return 0.5, nil
	}

	hamminess := 1.0
	spamminess := 1.0
	for _, v := range relevant {
		hamminess *= (1.0 - v)
		spamminess *= v
	}

	if hamminess == 1 && spamminess == 1 {
		return 0.5, nil
	}

	n := float64(len(relevant))
	hamminess = 1 - math.Pow(hamminess, 1/n)
	spamminess = 1 - math.Pow(spamminess, 1/n)

	probability := (hamminess - spamminess) / (hamminess + spamminess)
	probability = (1 + probability) / 2

	return probability, nil
}

// Learn records text as an example of category (Spam or Ham).
func (c *Classifier) Learn(text string, cat Category) error {
	return c.train(text, cat, storage.Learn)
}

// Unlearn removes a previously learned example from the database. The
// category must match the one used when the text was originally learned.
func (c *Classifier) Unlearn(text string, cat Category) error {
	return c.train(text, cat, storage.Unlearn)
}

// train is the shared implementation of Learn and Unlearn.
func (c *Classifier) train(text string, cat Category, act storage.Action) error {
	if text == "" {
		return ErrTextMissing
	}
	if cat != Spam && cat != Ham {
		return ErrCategoryInvalid
	}

	tokens, err := c.lexer.GetTokens(text)
	if err != nil {
		return err
	}

	return c.store.ProcessText(tokens, cat, act)
}

// tokenProbability returns the spam probability for a single token, consulting
// degenerate forms when the exact token is not found in the database.
func (c *Classifier) tokenProbability(
	word string,
	textsHam, textsSpam int64,
	tokenData *storage.TokenData,
) float64 {
	if data, ok := tokenData.Tokens[word]; ok {
		return c.robinsonProbability(data, textsHam, textsSpam)
	}

	if degenerates, ok := tokenData.Degenerates[word]; ok {
		rating := 0.5
		for _, data := range degenerates {
			candidate := c.robinsonProbability(data, textsHam, textsSpam)
			if math.Abs(0.5-candidate) > math.Abs(0.5-rating) {
				rating = candidate
			}
		}
		return rating
	}

	return c.config.RobX
}

// robinsonProbability applies Gary Robinson's formula to a single token's
// ham/spam counts. It weights the observed probability against the prior (RobX)
// scaled by the strength (RobS) and the total number of observations.
func (c *Classifier) robinsonProbability(data storage.TokenCount, textsHam, textsSpam int64) float64 {
	relHam := float64(data.CountHam)
	relSpam := float64(data.CountSpam)

	if textsHam > 0 {
		relHam = float64(data.CountHam) / float64(textsHam)
	}
	if textsSpam > 0 {
		relSpam = float64(data.CountSpam) / float64(textsSpam)
	}

	rating := relSpam / (relHam + relSpam)
	all := float64(data.CountHam + data.CountSpam)
	return (c.config.RobS*c.config.RobX + all*rating) / (c.config.RobS + all)
}

// sortedKeysByValueDesc returns the keys of m sorted by their float64 values
// in descending order. Insertion sort is used because UseRelevant is typically
// small (default 15).
func sortedKeysByValueDesc(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && m[keys[j]] > m[keys[j-1]]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
