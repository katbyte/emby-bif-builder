package readonly_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/emby-bif-builder/lib/readonly"
	"github.com/katbyte/emby-bif-builder/lib/servertest"
)

// A database opened read-only can be asked anything and written nothing, and its file is left exactly as it was.
func TestNothingCanBeWritten(t *testing.T) {
	t.Parallel()

	s := servertest.New(t)
	s.Episode("tv/Show - 01x01.mkv")
	s.Done()
	before, err := os.ReadFile(s.EmbyDB)
	if err != nil {
		t.Fatal(err)
	}

	db, err := readonly.Open(t.Context(), s.EmbyDB)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM MediaItems").Scan(&n); err != nil || n != 1 {
		t.Errorf("counted %d items (%v), want 1", n, err)
	}
	for _, q := range []string{"INSERT INTO MediaItems (type, Path) VALUES (5, 'x')", "DELETE FROM MediaItems", "CREATE TABLE x (y INT)"} {
		if _, err := db.ExecContext(t.Context(), q); err == nil {
			t.Errorf("%s was let through", q)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(s.EmbyDB)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the database's file changed")
	}

	// nothing beside it was made either: no journal, no lock
	entries, err := os.ReadDir(filepath.Dir(s.EmbyDB))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("beside the database are %v, want nothing but it", entries)
	}
}

// A file that is not a database is said when it is opened, not at the first question.
func TestAFileThatIsNotADatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "library.db")
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readonly.Open(t.Context(), path); err == nil || !strings.Contains(err.Error(), "cannot be read as a database") {
		t.Errorf("opening a file that is not a database gave %v", err)
	}
}
