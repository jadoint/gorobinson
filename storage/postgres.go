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

// upsertToken inserts, updates, deletes, or skips a single token row within
// the transaction.
func (p *Postgres) upsertToken(tx *sql.Tx, word string, count int, isSpam, isLearn bool) error {
	if isLearn {
		return p.learnToken(tx, word, int64(count), isSpam)
	}

	var current TokenCount
	q := fmt.Sprintf(
		`SELECT count_ham, count_spam FROM %s WHERE token = $1 FOR UPDATE`,
		p.table,
	)
	err := tx.QueryRow(q, word).Scan(&current.CountHam, &current.CountSpam)
	rowExists := true
	if errors.Is(err, sql.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return fmt.Errorf("storage/postgres: upsertToken select: %w", err)
	}

	mutation := applyCountMutation(current, rowExists, int64(count), isSpam, isLearn)
	if mutation.Delete {
		q = fmt.Sprintf(`DELETE FROM %s WHERE token = $1`, p.table)
		if _, err = tx.Exec(q, word); err != nil {
			return fmt.Errorf("storage/postgres: upsertToken delete: %w", err)
		}
	} else if mutation.Upsert && rowExists {
		q = fmt.Sprintf(
			`UPDATE %s SET count_ham = $1, count_spam = $2 WHERE token = $3`,
			p.table,
		)
		if _, err = tx.Exec(q, mutation.Count.CountHam, mutation.Count.CountSpam, word); err != nil {
			return fmt.Errorf("storage/postgres: upsertToken update: %w", err)
		}
	} else if mutation.Upsert {
		q = fmt.Sprintf(
			`INSERT INTO %s (token, count_ham, count_spam) VALUES ($1, $2, $3)`,
			p.table,
		)
		if _, err = tx.Exec(q, word, mutation.Count.CountHam, mutation.Count.CountSpam); err != nil {
			return fmt.Errorf("storage/postgres: upsertToken insert: %w", err)
		}
	}
	return nil
}

func (p *Postgres) learnToken(tx *sql.Tx, word string, count int64, isSpam bool) error {
	var q string
	if isSpam {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, 0, $2)
			ON CONFLICT (token) DO UPDATE
			SET count_spam = %s.count_spam + $2`,
			p.table, p.table,
		)
	} else {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, $2, 0)
			ON CONFLICT (token) DO UPDATE
			SET count_ham = %s.count_ham + $2`,
			p.table, p.table,
		)
	}

	if _, err := tx.Exec(q, word, count); err != nil {
		return fmt.Errorf("storage/postgres: learnToken: %w", err)
	}
	return nil
}

// updateInternals increments or decrements the internals row within the
// transaction.
func (p *Postgres) updateInternals(tx *sql.Tx, isSpam, isLearn bool) error {
	if isLearn {
		return p.learnInternals(tx, isSpam)
	}

	var current Internals
	q := fmt.Sprintf(
		`SELECT count_ham, count_spam FROM %s WHERE token = $1 FOR UPDATE`,
		p.table,
	)
	err := tx.QueryRow(q, MetaKey).Scan(&current.TextsHam, &current.TextsSpam)
	rowExists := true
	if errors.Is(err, sql.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return fmt.Errorf("storage/postgres: updateInternals select: %w", err)
	}

	counts := TokenCount{CountHam: current.TextsHam, CountSpam: current.TextsSpam}
	mutation := applyCountMutation(counts, rowExists, 1, isSpam, isLearn)
	if mutation.Delete {
		q = fmt.Sprintf(`DELETE FROM %s WHERE token = $1`, p.table)
		if _, err = tx.Exec(q, MetaKey); err != nil {
			return fmt.Errorf("storage/postgres: updateInternals delete: %w", err)
		}
	} else if mutation.Upsert && rowExists {
		q = fmt.Sprintf(
			`UPDATE %s SET count_ham = $1, count_spam = $2 WHERE token = $3`,
			p.table,
		)
		if _, err = tx.Exec(q, mutation.Count.CountHam, mutation.Count.CountSpam, MetaKey); err != nil {
			return fmt.Errorf("storage/postgres: updateInternals update: %w", err)
		}
	} else if mutation.Upsert {
		q = fmt.Sprintf(
			`INSERT INTO %s (token, count_ham, count_spam) VALUES ($1, $2, $3)`,
			p.table,
		)
		if _, err = tx.Exec(q, MetaKey, mutation.Count.CountHam, mutation.Count.CountSpam); err != nil {
			return fmt.Errorf("storage/postgres: updateInternals insert: %w", err)
		}
	}
	return nil
}

func (p *Postgres) learnInternals(tx *sql.Tx, isSpam bool) error {
	var q string
	if isSpam {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, 0, 1)
			ON CONFLICT (token) DO UPDATE
			SET count_spam = %s.count_spam + 1`,
			p.table, p.table,
		)
	} else {
		q = fmt.Sprintf(`
			INSERT INTO %s (token, count_ham, count_spam)
			VALUES ($1, 1, 0)
			ON CONFLICT (token) DO UPDATE
			SET count_ham = %s.count_ham + 1`,
			p.table, p.table,
		)
	}

	if _, err := tx.Exec(q, MetaKey); err != nil {
		return fmt.Errorf("storage/postgres: learnInternals: %w", err)
	}
	return nil
}
