// Package storage defines the Storage interface used by the gorobinson
// classifier and provides MySQL and PostgreSQL implementations.
package storage

// Category indicates whether a text is spam or ham.
type Category string

const (
	// Spam marks a text as spam during training.
	Spam Category = "spam"
	// Ham marks a text as legitimate (not spam) during training.
	Ham Category = "ham"
)

// Action indicates whether a training call should record or remove examples.
type Action string

const (
	// Learn records token counts for a text.
	Learn Action = "learn"
	// Unlearn removes previously recorded token counts for a text.
	Unlearn Action = "unlearn"
)

// Internals holds the aggregate counts of ham and spam texts that have been
// learned so far.
type Internals struct {
	TextsHam  int64
	TextsSpam int64
}

// TokenCount holds the per-token ham and spam occurrence counts.
type TokenCount struct {
	CountHam  int64
	CountSpam int64
}

// TokenData is the result of a storage Get call. It contains exact matches
// for requested tokens as well as degenerate matches for tokens not found in
// the database.
type TokenData struct {
	// Tokens maps an exact token string to its stored counts.
	Tokens map[string]TokenCount

	// Degenerates maps an original token string to a map of degenerate form →
	// counts for each degenerate form that was found in the database.
	Degenerates map[string]map[string]TokenCount
}

// Degenerator is the subset of the gorobinson.Degenerator interface that the
// storage layer needs in order to expand unknown tokens into candidate forms
// during a Get call. Keeping this interface here avoids an import cycle.
type Degenerator interface {
	Degenerate(words []string) map[string][]string
}

// Storage is the interface that all database backends must implement.
type Storage interface {
	// GetInternals returns the total numbers of ham and spam texts that have
	// been learned.
	GetInternals() (Internals, error)

	// Get fetches token data for the supplied list of token words. For tokens
	// not found in the database the degenerator is used to produce alternative
	// forms that are looked up instead.
	Get(tokens []string, deg Degenerator) (*TokenData, error)

	// ProcessText records or removes a set of token counts and updates the
	// internal ham/spam text counters.
	ProcessText(tokens map[string]int, cat Category, act Action) error
}

// MetaKey is the database key under which the ham/spam text counters are stored.
const MetaKey = "__meta"
