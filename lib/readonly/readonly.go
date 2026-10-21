// Package readonly opens a media server's SQLite database so that nothing can be written to it and the server never waits on ebb.
//
// The file is opened read-only and as if it could not change, so no lock is taken and the server's own writing goes on undisturbed, even over a
// network share. The cost is that what the server wrote in the last few minutes, and has not yet folded into the file, is not seen until it does,
// and that a question can be caught in the middle of the server folding it in: Retry asks again.
package readonly

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ncruces/go-sqlite3"

	// the SQLite driver, built into the program so it needs no C compiler and no library on the machine it runs on
	_ "github.com/ncruces/go-sqlite3/driver"
)

// Open opens the database at path, read-only and without taking any lock. A file that is not there is said now, rather than at the first question.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a folder, not a database", abs)
	}

	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	// one question at a time, each on a connection of its own, so none answers from pages read before the server last wrote to the file
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	// opening reads nothing, so the first question is what says whether the file is a database at all
	var tables int
	if err := Retry(ctx, func() error { return db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&tables) }); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("%s cannot be read as a database: %w", abs, err)
	}

	return db, nil
}

// Rows asks a question and hands each row of the answer to each, asking again from the start when the answer is caught half changed. start is
// called before each try, to forget what an earlier one handed over.
func Rows(ctx context.Context, db *sql.DB, start func(), each func(*sql.Rows) error, query string, args ...any) error {
	return Retry(ctx, func() error {
		start()

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			if err := each(rows); err != nil {
				return err
			}
		}

		return rows.Err()
	})
}

// Retry asks until the answer is whole, a few times over some seconds. A question asked while the server is folding what it wrote into the file
// can find the file half changed, which reads as damaged; asked again a moment later, it reads the file as it now is.
func Retry(ctx context.Context, ask func() error) error {
	return askAgain(ctx, 6, 200*time.Millisecond, ask)
}

// askAgain is Retry with how many tries and how long to wait first, each wait twice the last.
func askAgain(ctx context.Context, tries int, wait time.Duration, ask func() error) error {
	for try := 1; ; try++ {
		err := ask()
		if err == nil || !errors.Is(err, sqlite3.CORRUPT) || try >= tries {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}
