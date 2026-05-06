package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
)

// Postgres implements Storage using a PostgreSQL database.
type Postgres struct {
	db    *sql.DB
	table string
}

// NewPostgres returns a Postgres-backed Storage using db. table is the name
// of the word-list table.
func NewPostgres(db *sql.DB, table string) *Postgres {
	return &Postgres{db: db, table: table}
}

// GetInternals implements Storage.
func (p *Postgres) GetInternals() (Internals, error) {
	var internals Internals
	q := fmt.Sprintf(
		`SELECT count_ham, count_spam FROM %s WHERE token = $1`,
		p.table,
	)
	row := p.db.QueryRow(q, MetaKey)
	err := row.Scan(&internals.TextsHam, &internals.TextsSpam)
	if errors.Is(err, sql.ErrNoRows) {
		return Internals{}, nil
	}
	if err != nil {
		return Internals{}, fmt.Errorf("storage/postgres: GetInternals: %w", err)
	}
	return internals, nil
}

// Get implements Storage.
func (p *Postgres) Get(tokens []string, deg Degenerator) (*TokenData, error) {
	if len(tokens) == 0 {
		return &TokenData{
			Tokens:      make(map[string]TokenCount),
			Degenerates: make(map[string]map[string]TokenCount),
		}, nil
	}

	found, err := p.fetchTokens(tokens)
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
			degFound, err := p.fetchTokens(forms)
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

// fetchTokens executes a single SELECT ... WHERE token IN (...) query.
func (p *Postgres) fetchTokens(words []string) (map[string]TokenCount, error) {
	if len(words) == 0 {
		return make(map[string]TokenCount), nil
	}

	placeholders := make([]string, len(words))
	args := make([]interface{}, len(words))
	for i, w := range words {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = w
	}

	q := fmt.Sprintf(
		`SELECT token, count_ham, count_spam FROM %s WHERE token IN (%s)`,
		p.table, strings.Join(placeholders, ","),
	)

	rows, err := p.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: fetchTokens: %w", err)
	}
	defer rows.Close()

	result := make(map[string]TokenCount, len(words))
	for rows.Next() {
		var token string
		var tc TokenCount
		if err := rows.Scan(&token, &tc.CountHam, &tc.CountSpam); err != nil {
			return nil, fmt.Errorf("storage/postgres: fetchTokens scan: %w", err)
		}
		result[token] = tc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage/postgres: fetchTokens rows: %w", err)
	}
	return result, nil
}

// ProcessText implements Storage.
func (p *Postgres) ProcessText(tokens map[string]int, cat Category, act Action) error {
	tx, err := p.db.Begin()
	if err != nil {
		return fmt.Errorf("storage/postgres: ProcessText begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	isSpam := cat == Spam
	isLearn := act == Learn

	for word, count := range tokens {
		if err = p.upsertToken(tx, word, count, isSpam, isLearn); err != nil {
			return err
		}
	}

	if err = p.updateInternals(tx, isSpam, isLearn); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("storage/postgres: ProcessText commit: %w", err)
	}
	return nil
}

// upsertToken inserts or updates a single token row within the transaction.
func (p *Postgres) upsertToken(tx *sql.Tx, word string, count int, isSpam, isLearn bool) error {
	delta := int64(count)
	if !isLearn {
		delta = -delta
	}

	var q string
	if isSpam {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, 0, GREATEST(0, $2))
			ON CONFLICT (token) DO UPDATE
			SET count_spam = GREATEST(0, %s.count_spam + $2)`,
			p.table, p.table,
		)
	} else {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, GREATEST(0, $2), 0)
			ON CONFLICT (token) DO UPDATE
			SET count_ham = GREATEST(0, %s.count_ham + $2)`,
			p.table, p.table,
		)
	}

	if _, err := tx.Exec(q, word, delta); err != nil {
		return fmt.Errorf("storage/postgres: upsertToken: %w", err)
	}
	return nil
}

// updateInternals increments or decrements the internals row within the
// transaction.
func (p *Postgres) updateInternals(tx *sql.Tx, isSpam, isLearn bool) error {
	delta := int64(1)
	if !isLearn {
		delta = -1
	}

	var q string
	if isSpam {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, 0, GREATEST(0, $2))
			ON CONFLICT (token) DO UPDATE
			SET count_spam = GREATEST(0, %s.count_spam + $2)`,
			p.table, p.table,
		)
	} else {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, GREATEST(0, $2), 0)
			ON CONFLICT (token) DO UPDATE
			SET count_ham = GREATEST(0, %s.count_ham + $2)`,
			p.table, p.table,
		)
	}

	if _, err := tx.Exec(q, MetaKey, delta); err != nil {
		return fmt.Errorf("storage/postgres: updateInternals: %w", err)
	}
	return nil
}
