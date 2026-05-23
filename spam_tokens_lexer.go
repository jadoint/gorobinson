package gorobinson

import spamtokens "github.com/jadoint/spam-tokens"

// SpamTokensLexer adapts github.com/jadoint/spam-tokens to the Lexer
// interface. It is the default lexer used by New because it preserves
// Unicode-aware spam signals such as CJK script flags, URL components, and
// normalized HTML tag tokens.
type SpamTokensLexer struct {
	opts spamtokens.Options
}

// NewSpamTokensLexer constructs the default spam-tokens-backed lexer.
func NewSpamTokensLexer() *SpamTokensLexer {
	return &SpamTokensLexer{opts: spamtokens.DefaultOptions()}
}

// GetTokens implements Lexer.
func (l *SpamTokensLexer) GetTokens(text string) (map[string]int, error) {
	if text == "" {
		return nil, ErrLexerTextEmpty
	}
	tokens := spamtokens.Tokenize(text, l.opts)
	if len(tokens) == 0 {
		tokens = map[string]int{NoTokensKey: 1}
	}
	return tokens, nil
}
