//go:build integration

// Integration tests for Get against real MySQL and Postgres servers. They use
// only exported API, so the same file runs against older releases. Each
// backend's tests run when its DSN is set and skip otherwise:
//
//	GOROBINSON_IT_MYSQL_DSN=... GOROBINSON_IT_PG_DSN=... go test -tags integration ./storage/
//
// Every test creates its own table and drops it when it ends.
package storage_test

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jadoint/gorobinson/storage"
)

// backend is one database server under test.
type backend struct {
	name   string
	env    string
	driver string
	ddl    string // %s is the table name
	quote  func(table string) string
	ph     func(n int) string // the n-th placeholder, 1-based
	open   func(db *sql.DB, table string) storage.Storage
}

var backends = []backend{
	{
		name:   "mysql",
		env:    "GOROBINSON_IT_MYSQL_DSN",
		driver: "mysql",
		ddl: "CREATE TABLE `%s` (`token` varchar(255) CHARACTER SET utf8mb4 COLLATE " +
			"utf8mb4_bin NOT NULL DEFAULT '', `count_ham` int unsigned NOT NULL DEFAULT " +
			"'0', `count_spam` int unsigned NOT NULL DEFAULT '0', PRIMARY KEY (`token`)) " +
			"ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci " +
			"ROW_FORMAT=COMPRESSED",
		quote: func(table string) string { return "`" + table + "`" },
		ph:    func(int) string { return "?" },
		open: func(db *sql.DB, table string) storage.Storage {
			return storage.NewMySQL(db, table)
		},
	},
	{
		name:   "postgres",
		env:    "GOROBINSON_IT_PG_DSN",
		driver: "postgres",
		ddl: "CREATE TABLE %s (token TEXT NOT NULL, count_ham BIGINT NOT NULL DEFAULT " +
			"0, count_spam BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (token))",
		quote: func(table string) string { return table },
		ph:    func(n int) string { return fmt.Sprintf("$%d", n) },
		open: func(db *sql.DB, table string) storage.Storage {
			return storage.NewPostgres(db, table)
		},
	},
}

// eachBackend runs fn as one subtest per backend, skipping a backend whose
// DSN is unset.
func eachBackend(t *testing.T, fn func(t *testing.T, b backend, db *sql.DB)) {
	t.Helper()
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			dsn := os.Getenv(b.env)
			if dsn == "" {
				t.Skipf("%s is unset", b.env)
			}
			db, err := sql.Open(b.driver, dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Ping(); err != nil {
				t.Fatalf("ping: %v", err)
			}
			fn(t, b, db)
		})
	}
}

// createTable makes a table of this test's own from the production DDL, fills
// it with rows, and drops it when the test ends.
func createTable(t *testing.T, b backend, db *sql.DB, rows map[string]storage.TokenCount) string {
	t.Helper()
	table := fmt.Sprintf("gorobinson_it_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf(b.ddl, table)); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DROP TABLE " + b.quote(table)); err != nil {
			t.Errorf("drop %s: %v", table, err)
		}
	})

	tokens := make([]string, 0, len(rows))
	for tok := range rows {
		tokens = append(tokens, tok)
	}
	const batch = 1000
	for start := 0; start < len(tokens); start += batch {
		end := min(start+batch, len(tokens))
		values := make([]string, 0, end-start)
		args := make([]any, 0, 3*(end-start))
		for i, tok := range tokens[start:end] {
			values = append(values, fmt.Sprintf("(%s,%s,%s)", b.ph(3*i+1), b.ph(3*i+2), b.ph(3*i+3)))
			args = append(args, tok, rows[tok].CountHam, rows[tok].CountSpam)
		}
		q := fmt.Sprintf("INSERT INTO %s (token, count_ham, count_spam) VALUES %s",
			b.quote(table), strings.Join(values, ","))
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("insert into %s: %v", table, err)
		}
	}
	return table
}

// caseDegenerator returns each word's upper- and lower-case forms.
type caseDegenerator struct{}

func (caseDegenerator) Degenerate(words []string) map[string][]string {
	result := make(map[string][]string, len(words))
	for _, w := range words {
		result[w] = []string{strings.ToUpper(w), strings.ToLower(w)}
	}
	return result
}

func countFor(i int) storage.TokenCount {
	return storage.TokenCount{CountHam: int64(i%1000 + 1), CountSpam: int64(i % 997)}
}

// checkGet calls Get and compares the result with the expected maps.
func checkGet(t *testing.T, st storage.Storage, tokens []string, wantTokens map[string]storage.TokenCount, wantDegens map[string]map[string]storage.TokenCount) {
	t.Helper()
	got, err := st.Get(tokens, caseDegenerator{})
	if err != nil {
		t.Fatalf("Get(%d tokens): %v", len(tokens), err)
	}
	if !reflect.DeepEqual(got.Tokens, wantTokens) {
		t.Errorf("Get(%d tokens): Tokens has %d entries, want %d, or counts differ",
			len(tokens), len(got.Tokens), len(wantTokens))
	}
	if !reflect.DeepEqual(got.Degenerates, wantDegens) {
		t.Errorf("Get(%d tokens): Degenerates has %d entries, want %d, or counts differ",
			len(tokens), len(got.Degenerates), len(wantDegens))
	}
}

// TestIntegrationGetOver65535 looks up 70,000 distinct tokens in one Get, more
// than one statement may name.
func TestIntegrationGetOver65535(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend, db *sql.DB) {
		rows := make(map[string]storage.TokenCount, 100000)
		for i := range 90000 {
			rows[fmt.Sprintf("tok%06d", i)] = countFor(i)
		}
		for i := 90000; i < 100000; i++ {
			rows[fmt.Sprintf("TOK%06d", i)] = countFor(i)
		}
		table := createTable(t, b, db, rows)

		tokens := make([]string, 0, 70000)
		wantTokens := make(map[string]storage.TokenCount, 40000)
		wantDegens := make(map[string]map[string]storage.TokenCount, 10000)
		for i := range 40000 {
			tok := fmt.Sprintf("tok%06d", i)
			tokens = append(tokens, tok)
			wantTokens[tok] = countFor(i)
		}
		for i := 100000; i < 120000; i++ {
			tokens = append(tokens, fmt.Sprintf("tok%06d", i))
		}
		for i := 90000; i < 100000; i++ {
			tok := fmt.Sprintf("tok%06d", i)
			tokens = append(tokens, tok)
			wantDegens[tok] = map[string]storage.TokenCount{fmt.Sprintf("TOK%06d", i): countFor(i)}
		}

		checkGet(t, b.open(db, table), tokens, wantTokens, wantDegens)
	})
}

// TestIntegrationGetChunkBoundaries looks up token counts either side of the
// chunk size, with one token of 40 four-byte runes among them.
func TestIntegrationGetChunkBoundaries(t *testing.T) {
	// U+10428 DESERET SMALL LETTER LONG I upper-cases to U+10400; both are
	// four bytes in UTF-8, so each is 160 bytes, spam-tokens' longest token.
	longLower := strings.Repeat("\U00010428", 40)
	longUpper := strings.Repeat("\U00010400", 40)

	const maxTokens = 2001
	eachBackend(t, func(t *testing.T, b backend, db *sql.DB) {
		// Even i are stored; odd i are absent, and for i%6 == 3 their
		// upper-case form is stored.
		rows := make(map[string]storage.TokenCount, maxTokens)
		for i := range maxTokens {
			switch {
			case i%2 == 0:
				rows[fmt.Sprintf("b%06d", i)] = countFor(i)
			case i%6 == 3:
				rows[fmt.Sprintf("B%06d", i)] = countFor(i)
			}
		}
		rows[longUpper] = countFor(maxTokens)
		table := createTable(t, b, db, rows)
		st := b.open(db, table)

		for _, n := range []int{999, 1000, 1001, 2001} {
			t.Run(fmt.Sprint(n), func(t *testing.T) {
				tokens := make([]string, 0, n)
				wantTokens := make(map[string]storage.TokenCount)
				wantDegens := make(map[string]map[string]storage.TokenCount)
				for i := range n - 1 {
					tok := fmt.Sprintf("b%06d", i)
					tokens = append(tokens, tok)
					switch {
					case i%2 == 0:
						wantTokens[tok] = countFor(i)
					case i%6 == 3:
						wantDegens[tok] = map[string]storage.TokenCount{fmt.Sprintf("B%06d", i): countFor(i)}
					}
				}
				tokens = append(tokens, longLower)
				wantDegens[longLower] = map[string]storage.TokenCount{longUpper: countFor(maxTokens)}

				checkGet(t, st, tokens, wantTokens, wantDegens)
			})
		}
	})
}
