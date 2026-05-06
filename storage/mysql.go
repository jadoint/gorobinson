package storage

import (
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

// MySQL implements Storage using a MySQL database.
type MySQL struct {
	db    *sql.DB
	table string
}

// NewMySQL returns a MySQL-backed Storage using db. table is the name of the
// word-list table.
func NewMySQL(db *sql.DB, table string) *MySQL {
	return &MySQL{db: db, table: table}
}

// GetInternals implements Storage.
func (m *MySQL) GetInternals() (Internals, error) {
	var internals Internals
	q := fmt.Sprintf(
		"SELECT count_ham, count_spam FROM `%s` WHERE token = ?",
		m.table,
	)
	row := m.db.QueryRow(q, MetaKey)
	err := row.Scan(&internals.TextsHam, &internals.TextsSpam)
	if errors.Is(err, sql.ErrNoRows) {
		return Internals{}, nil
	}
	if err != nil {
		return Internals{}, fmt.Errorf("storage/mysql: GetInternals: %w", err)
	}
	return internals, nil
}

// Get implements Storage.
func (m *MySQL) Get(tokens []string, deg Degenerator) (*TokenData, error) {
	if len(tokens) == 0 {
		return &TokenData{
			Tokens:      make(map[string]TokenCount),
			Degenerates: make(map[string]map[string]TokenCount),
		}, nil
	}

	found, err := m.fetchTokens(tokens)
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
			degFound, err := m.fetchTokens(forms)
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

// fetchTokens executes a single SELECT ... WHERE token IN (...) query and
// returns the results as a map.
func (m *MySQL) fetchTokens(words []string) (map[string]TokenCount, error) {
	if len(words) == 0 {
		return make(map[string]TokenCount), nil
	}

	args := make([]interface{}, len(words))
	placeholders := make([]byte, 0, len(words)*2)
	for i, w := range words {
		args[i] = w
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}

	q := fmt.Sprintf(
		"SELECT token, count_ham, count_spam FROM `%s` WHERE token IN (%s)",
		m.table, string(placeholders),
	)

	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage/mysql: fetchTokens: %w", err)
	}
	defer rows.Close()

	result := make(map[string]TokenCount, len(words))
	for rows.Next() {
		var token string
		var tc TokenCount
		if err := rows.Scan(&token, &tc.CountHam, &tc.CountSpam); err != nil {
			return nil, fmt.Errorf("storage/mysql: fetchTokens scan: %w", err)
		}
		result[token] = tc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage/mysql: fetchTokens rows: %w", err)
	}
	return result, nil
}

// ProcessText implements Storage.
func (m *MySQL) ProcessText(tokens map[string]int, cat Category, act Action) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("storage/mysql: ProcessText begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	isSpam := cat == Spam
	isLearn := act == Learn

	for word, count := range tokens {
		if err = m.upsertToken(tx, word, count, isSpam, isLearn); err != nil {
			return err
		}
	}

	if err = m.updateInternals(tx, isSpam, isLearn); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("storage/mysql: ProcessText commit: %w", err)
	}
	return nil
}

// upsertToken inserts or updates a single token row within the transaction.
func (m *MySQL) upsertToken(tx *sql.Tx, word string, count int, isSpam, isLearn bool) error {
	var current TokenCount
	q := fmt.Sprintf(
		"SELECT count_ham, count_spam FROM `%s` WHERE token = ? FOR UPDATE",
		m.table,
	)
	err := tx.QueryRow(q, word).Scan(&current.CountHam, &current.CountSpam)
	rowExists := true
	if errors.Is(err, sql.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return fmt.Errorf("storage/mysql: upsertToken select: %w", err)
	}

	delta := int64(count)
	if !isLearn {
		delta = -delta
	}

	if isSpam {
		current.CountSpam += delta
	} else {
		current.CountHam += delta
	}
	if current.CountHam < 0 {
		current.CountHam = 0
	}
	if current.CountSpam < 0 {
		current.CountSpam = 0
	}

	if rowExists {
		q = fmt.Sprintf(
			"UPDATE `%s` SET count_ham = ?, count_spam = ? WHERE token = ?",
			m.table,
		)
		if _, err = tx.Exec(q, current.CountHam, current.CountSpam, word); err != nil {
			return fmt.Errorf("storage/mysql: upsertToken update: %w", err)
		}
	} else {
		q = fmt.Sprintf(
			"INSERT INTO `%s` (token, count_ham, count_spam) VALUES (?, ?, ?)",
			m.table,
		)
		if _, err = tx.Exec(q, word, current.CountHam, current.CountSpam); err != nil {
			return fmt.Errorf("storage/mysql: upsertToken insert: %w", err)
		}
	}
	return nil
}

// updateInternals increments or decrements the internals row within the
// transaction.
func (m *MySQL) updateInternals(tx *sql.Tx, isSpam, isLearn bool) error {
	var current Internals
	q := fmt.Sprintf(
		"SELECT count_ham, count_spam FROM `%s` WHERE token = ? FOR UPDATE",
		m.table,
	)
	err := tx.QueryRow(q, MetaKey).Scan(&current.TextsHam, &current.TextsSpam)
	rowExists := true
	if errors.Is(err, sql.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return fmt.Errorf("storage/mysql: updateInternals select: %w", err)
	}

	delta := int64(1)
	if !isLearn {
		delta = -1
	}

	if isSpam {
		current.TextsSpam += delta
	} else {
		current.TextsHam += delta
	}
	if current.TextsHam < 0 {
		current.TextsHam = 0
	}
	if current.TextsSpam < 0 {
		current.TextsSpam = 0
	}

	if rowExists {
		q = fmt.Sprintf(
			"UPDATE `%s` SET count_ham = ?, count_spam = ? WHERE token = ?",
			m.table,
		)
		if _, err = tx.Exec(q, current.TextsHam, current.TextsSpam, MetaKey); err != nil {
			return fmt.Errorf("storage/mysql: updateInternals update: %w", err)
		}
	} else {
		q = fmt.Sprintf(
			"INSERT INTO `%s` (token, count_ham, count_spam) VALUES (?, ?, ?)",
			m.table,
		)
		if _, err = tx.Exec(q, MetaKey, current.TextsHam, current.TextsSpam); err != nil {
			return fmt.Errorf("storage/mysql: updateInternals insert: %w", err)
		}
	}
	return nil
}
