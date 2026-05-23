package gorobinson

import (
	"errors"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Lexer tokenises input text into a map of token → occurrence count.
type Lexer interface {
	// GetTokens returns a map of token string to the number of times it
	// appeared in text. It returns an error if text is empty or otherwise
	// unparseable.
	GetTokens(text string) (map[string]int, error)
}

// ErrLexerTextEmpty is returned when an empty string is passed to GetTokens.
var ErrLexerTextEmpty = errors.New("lexer: text must not be empty")

// NoTokensKey is the sentinel token inserted when no valid tokens can be
// extracted from a text. It is stored in the database like any other token
// and can be queried directly.
const NoTokensKey = "__no_tokens"

// LexerConfig holds the configuration for StandardLexer.
type LexerConfig struct {
	// MinSize is the minimum number of bytes a token must have to be kept
	// (default 3).
	MinSize int

	// MaxSize is the maximum number of bytes a token may have to be kept
	// (default 30).
	MaxSize int

	// AllowNumbers controls whether purely numeric tokens are kept
	// (default false).
	AllowNumbers bool

	// GetURIs controls whether URI-like strings are extracted before the raw
	// split pass (default true).
	GetURIs bool

	// ExtractHTMLTags extracts HTML tags without removing them from the input,
	// so each tag is tokenised in addition to the surrounding text (default true).
	ExtractHTMLTags bool

	// GetHTML extracts full HTML tags and removes them from the input before
	// the raw split (default false).
	GetHTML bool

	// GetBBCode extracts BBCode tags and removes them from the input before
	// the raw split (default false).
	GetBBCode bool
}

// StandardLexerConfig returns a LexerConfig with sensible defaults.
func StandardLexerConfig() LexerConfig {
	return LexerConfig{
		MinSize:         3,
		MaxSize:         30,
		AllowNumbers:    false,
		GetURIs:         true,
		ExtractHTMLTags: true,
		GetHTML:         false,
		GetBBCode:       false,
	}
}

// Compiled regular expressions used by StandardLexer. These are package-level
// variables so they are only compiled once.
var (
	reRawSplit = regexp.MustCompile(`[\s,\.\/"\:;\|<>\-_\[\]{}\+=\)\(\*\&\^%]+`)
	reURIs     = regexp.MustCompile(`[A-Za-z0-9_\-]*\.[A-Za-z0-9_\-\.]+`)
	reHTML     = regexp.MustCompile(`<.+?>`)
	reBBCode   = regexp.MustCompile(`\[.+?\]`)
	reTagName  = regexp.MustCompile(`^(.+?)\s`)
	reNumbers  = regexp.MustCompile(`^[0-9]+$`)
)

// StandardLexer is the legacy generic Lexer implementation. New uses
// SpamTokensLexer by default; construct StandardLexer directly when you need
// the older generic tokenization rules.
type StandardLexer struct {
	config LexerConfig
}

// NewStandardLexer constructs a StandardLexer with the supplied config.
func NewStandardLexer(cfg LexerConfig) *StandardLexer {
	return &StandardLexer{config: cfg}
}

// GetTokens implements Lexer. It decodes HTML entities, extracts URI-like
// strings, HTML tags, and BBCode if configured, then performs a raw split on
// the remaining text.
func (l *StandardLexer) GetTokens(text string) (map[string]int, error) {
	if text == "" {
		return nil, ErrLexerTextEmpty
	}

	processed := html.UnescapeString(text)
	tokens := make(map[string]int)

	if l.config.GetURIs {
		processed = l.extractURIs(processed, tokens)
	}
	if l.config.ExtractHTMLTags {
		l.extractHTMLTags(processed, tokens)
	}
	if l.config.GetHTML {
		processed = l.extractMarkup(processed, reHTML, tokens)
	}
	if l.config.GetBBCode {
		processed = l.extractMarkup(processed, reBBCode, tokens)
	}

	l.rawSplit(processed, tokens)

	if len(tokens) == 0 {
		tokens[NoTokensKey] = 1
	}

	return tokens, nil
}

// isValid returns true when token passes all configured filters.
func (l *StandardLexer) isValid(token string) bool {
	if strings.HasPrefix(token, "__") {
		return false
	}
	n := utf8.RuneCountInString(token)
	if n < l.config.MinSize || n > l.config.MaxSize {
		return false
	}
	if !l.config.AllowNumbers && reNumbers.MatchString(token) {
		return false
	}
	return true
}

// addToken validates token and increments its count in tokens.
func (l *StandardLexer) addToken(token string, tokens map[string]int) {
	if !l.isValid(token) {
		return
	}
	tokens[token]++
}

// extractURIs finds URI-like strings in text, adds each one (and its
// constituent raw-split parts) to tokens, then returns text with those
// substrings removed.
func (l *StandardLexer) extractURIs(text string, tokens map[string]int) string {
	matches := reURIs.FindAllString(text, -1)
	for _, word := range matches {
		word = strings.TrimRight(word, ".")
		l.addToken(word, tokens)
		l.rawSplit(word, tokens)
		text = strings.ReplaceAll(text, word, "")
	}
	return text
}

// extractHTMLTags extracts HTML tags from text without removing them, so each
// tag is tokenised in addition to the surrounding plain text.
func (l *StandardLexer) extractHTMLTags(text string, tokens map[string]int) {
	matches := reHTML.FindAllString(text, -1)
	for _, word := range matches {
		if strings.ContainsRune(word, ' ') {
			if m := reTagName.FindStringSubmatch(word); m != nil {
				word = m[1] + "...>"
			}
		}
		l.addToken(word, tokens)
	}
}

// extractMarkup extracts markup (HTML or BBCode) using re, adds each match to
// tokens, and returns text with those substrings removed.
func (l *StandardLexer) extractMarkup(text string, re *regexp.Regexp, tokens map[string]int) string {
	matches := re.FindAllString(text, -1)
	for _, word := range matches {
		actual := word
		if strings.ContainsRune(word, ' ') {
			if m := reTagName.FindStringSubmatch(word); m != nil {
				actual = m[1]
				word = actual + "..." + string([]rune(word)[utf8.RuneCountInString(word)-1:])
			}
		}
		l.addToken(word, tokens)
		text = strings.ReplaceAll(text, actual, "")
	}
	return text
}

// rawSplit splits text on a common delimiter set and adds each part to tokens.
func (l *StandardLexer) rawSplit(text string, tokens map[string]int) {
	parts := reRawSplit.Split(text, -1)
	for _, word := range parts {
		l.addToken(word, tokens)
	}
}
