# gorobinson

`gorobinson` is a spam classification library for Go implementing **Gary Robinson's algorithm**, a refinement of the Naive Bayesian approach.

Unlike standard Naive Bayesian classifiers, Robinson's method handles rare tokens more effectively by weighting each token's probability against a configurable prior using a strength parameter. The most significant tokens are then combined via a Fisher-derived geometric mean to produce a balanced spam probability.

## Features

*   **Robinson's Formula:** Superior handling of sparse data (rare tokens).
*   **Flexible Lexer:** Supports HTML entity decoding, URI extraction, and BBCode/HTML tag filtering.
*   **Degenerator:** Automatically checks word variations (case-folding, punctuation stripping) to find matches even for unseen tokens.
*   **Database Backends:** Built-in support for MySQL and PostgreSQL.

## Prerequisites

You must create the word-list table in your database before using the classifier. The table name is passed to the storage constructor and can be anything you choose.

### MySQL Schema

```sql
CREATE TABLE spam_tokens (
    token      VARCHAR(255) NOT NULL,
    count_ham  INT UNSIGNED NOT NULL DEFAULT 0,
    count_spam INT UNSIGNED NOT NULL DEFAULT 0,
    PRIMARY KEY (token)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=COMPRESSED;
```

### PostgreSQL Schema

```sql
CREATE TABLE spam_tokens (
    token      TEXT   NOT NULL,
    count_ham  BIGINT NOT NULL DEFAULT 0,
    count_spam BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (token)
);
```

## Installation

```bash
go get github.com/jadoint/gorobinson
```

## Usage

### 1. Initialize the Classifier

```go
import (
    "database/sql"
    _ "github.com/lib/pq" // or mysql driver
    "github.com/jadoint/gorobinson"
    "github.com/jadoint/gorobinson/storage"
)

func main() {
    db, _ := sql.Open("postgres", "postgres://user:pass@localhost/dbname?sslmode=disable")
    store := storage.NewPostgres(db, "spam_tokens")
    classifier := gorobinson.New(store)
}
```

### 2. Classify Text

The `Classify` method returns a probability between `0.0` (definitely ham) and `1.0` (definitely spam). A result of `0.5` indicates a neutral or unknown result.

```go
score, err := classifier.Classify("Get rich quick! Click here for a free prize!")
if err != nil {
    // handle error
}

if score > 0.9 {
    fmt.Println("This is definitely spam.")
} else if score < 0.1 {
    fmt.Println("This is definitely ham.")
} else {
    fmt.Printf("Uncertain result: %.2f\n", score)
}
```

### 3. Training (Learn and Unlearn)

To improve accuracy, train the classifier with examples of both spam and ham.

```go
// Teach the classifier what spam looks like
classifier.Learn("Buy cheap watches now!", gorobinson.Spam)

// Teach the classifier what legitimate mail looks like
classifier.Learn("Hey, are we still meeting for lunch at 12?", gorobinson.Ham)

// If you made a mistake, you can unlearn a specific text
classifier.Unlearn("This was actually not spam", gorobinson.Spam)
```

## Configuration

You can tune the algorithm using functional options:

```go
classifier := gorobinson.New(store,
    gorobinson.WithRobS(0.3),        // Strength of the prior
    gorobinson.WithRobX(0.5),        // Assumed probability for unseen tokens
    gorobinson.WithUseRelevant(15),  // Max tokens to consider per classification
    gorobinson.WithMinDev(0.2),      // Min deviation from 0.5 to be considered significant
)
```

## License

This project is licensed under the MIT License.
