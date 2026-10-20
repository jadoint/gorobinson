package storage

import (
	"maps"
	"sort"
)

// lookupChunkSize is the most words one lookup statement names. It keeps
// every statement a range read on the token table's primary key, well under
// the 65,535 placeholders MySQL server-side prepares and Postgres bind
// parameters allow.
const lookupChunkSize = 1000

// fetchFunc runs one backend statement over 1 to lookupChunkSize words and
// returns the stored counts keyed by the token string the database returned.
type fetchFunc func(words []string) (map[string]TokenCount, error)

// fetchChunked looks words up lookupChunkSize at a time, in their given
// order, and merges every chunk's result into one map. Zero words makes no
// call. The first error is returned unchanged.
func fetchChunked(words []string, fetch fetchFunc) (map[string]TokenCount, error) {
	result := make(map[string]TokenCount, len(words))
	for start := 0; start < len(words); start += lookupChunkSize {
		end := min(start+lookupChunkSize, len(words))
		chunk, err := fetch(words[start:end])
		if err != nil {
			return nil, err
		}
		maps.Copy(result, chunk)
	}
	return result, nil
}

// getTokenData implements Storage.Get over a backend's one-statement fetch.
//
// The token pass looks every token up in chunks. The form pass then asks deg
// once for the forms of every token the first pass did not find, and looks
// all of those forms up together in chunks, each form once, skipping forms
// that are themselves tokens because the token pass already answered them.
// Each degenerate entry then takes the counts of its own forms by exact form
// string. Chunks run one after another; there is no transaction across them.
func getTokenData(tokens []string, deg Degenerator, fetch fetchFunc) (*TokenData, error) {
	found, err := fetchChunked(tokens, fetch)
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
	if len(missing) == 0 || deg == nil {
		return &TokenData{Tokens: found, Degenerates: degenerates}, nil
	}

	degMap := deg.Degenerate(missing)

	isToken := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		isToken[t] = struct{}{}
	}

	// Keys in missing's order, then any key Degenerate added, sorted, so one
	// input always issues the same statements.
	keys := make([]string, 0, len(degMap))
	seenKey := make(map[string]struct{}, len(degMap))
	for _, k := range missing {
		if _, ok := degMap[k]; !ok {
			continue
		}
		if _, dup := seenKey[k]; dup {
			continue
		}
		seenKey[k] = struct{}{}
		keys = append(keys, k)
	}
	extra := len(keys)
	for k := range degMap {
		if _, ok := seenKey[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys[extra:])

	var toFetch []string
	queued := make(map[string]struct{})
	for _, k := range keys {
		for _, f := range degMap[k] {
			if _, ok := isToken[f]; ok {
				continue
			}
			if _, ok := queued[f]; ok {
				continue
			}
			queued[f] = struct{}{}
			toFetch = append(toFetch, f)
		}
	}

	formFound, err := fetchChunked(toFetch, fetch)
	if err != nil {
		return nil, err
	}

	for k, forms := range degMap {
		var sub map[string]TokenCount
		for _, f := range forms {
			var tc TokenCount
			var ok bool
			if _, tok := isToken[f]; tok {
				tc, ok = found[f]
			} else {
				tc, ok = formFound[f]
			}
			if !ok {
				continue
			}
			if sub == nil {
				sub = make(map[string]TokenCount)
			}
			sub[f] = tc
		}
		if sub != nil {
			degenerates[k] = sub
		}
	}

	return &TokenData{Tokens: found, Degenerates: degenerates}, nil
}
