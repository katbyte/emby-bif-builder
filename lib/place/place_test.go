package place_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/emby-bif-builder/lib/place"
)

// files is the names in a folder.
func files(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// A BIF is written whole at its name, readable by others, with nothing left beside it, and the record says so.
func TestABIFIsWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rec, err := place.OpenRecord(filepath.Join(t.TempDir(), "changes.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "S - 01x01-320-10.bif")

	changes, err := place.Put(target, []byte("a whole BIF"), false, nil, rec, "no BIF")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0] != (place.Change{What: "wrote", Path: target}) {
		t.Errorf("changes %+v", changes)
	}

	got, err := os.ReadFile(target) //nolint:gosec // the test's own file
	if err != nil || string(got) != "a whole BIF" {
		t.Errorf("the BIF holds %q (%v)", got, err)
	}
	if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("the BIF's permissions are %v (%v), want others able to read it", fi.Mode().Perm(), err)
	}
	if names := files(t, dir); len(names) != 1 {
		t.Errorf("the folder holds %q, want the BIF alone", names)
	}

	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fields := strings.Split(strings.TrimSpace(string(raw)), "\t"); len(fields) != 4 || fields[1] != "wrote" || fields[2] != target || fields[3] != "no BIF" {
		t.Errorf("the record reads %q", raw)
	}
}

// A BIF that appeared since the folder was read is left as it is, unless writing over it was meant.
func TestABIFThatIsThereIsLeft(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "S - 01x01-320-10.bif")
	if err := os.WriteFile(target, []byte("Emby's"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := place.Put(target, []byte("ebb's"), false, nil, nil, ""); err == nil || !strings.Contains(err.Error(), "left as it is") {
		t.Errorf("writing over a BIF that appeared gave %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "Emby's" { //nolint:gosec // the test's own file
		t.Errorf("the BIF there now holds %q", got)
	}
	if names := files(t, dir); len(names) != 1 {
		t.Errorf("the folder holds %q, want the BIF there alone", names)
	}

	changes, err := place.Put(target, []byte("ebb's"), true, nil, nil, "")
	if err != nil || len(changes) != 1 || changes[0].What != "replaced" {
		t.Errorf("writing over it as meant gave %+v (%v)", changes, err)
	}
}

// The BIFs a new one replaces are removed once it is in place, and each removal is recorded.
func TestTheBIFsReplacedAreRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := filepath.Join(dir, "S - 01x01.bif")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := place.OpenRecord(filepath.Join(t.TempDir(), "changes.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Close() }()

	target := filepath.Join(dir, "S - 01x01-320-10.bif")
	changes, err := place.Put(target, []byte("new"), true, []string{old}, rec, "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[1] != (place.Change{What: "removed", Path: old}) {
		t.Errorf("changes %+v", changes)
	}
	if names := files(t, dir); len(names) != 1 || names[0] != filepath.Base(target) {
		t.Errorf("the folder holds %q, want the new BIF alone", names)
	}

	raw, err := os.ReadFile(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\tremoved\t"+old+"\treplaced by S - 01x01-320-10.bif\n") {
		t.Errorf("the record does not hold the removal: %q", raw)
	}
}

// A folder that cannot be written to is said, and nothing is left in it.
func TestAFolderThatCannotBeWritten(t *testing.T) {
	t.Parallel()

	if _, err := place.Put(filepath.Join(t.TempDir(), "no-such-folder", "x.bif"), []byte("x"), false, nil, nil, ""); err == nil {
		t.Error("a BIF was written into a folder that is not there")
	}

	none, err := place.OpenRecord("")
	if err != nil || none != nil {
		t.Errorf("no record gave %v (%v)", none, err)
	}
	if err := none.Close(); err != nil {
		t.Errorf("closing no record: %v", err)
	}
}

// A file nothing needs is removed and recorded, and one already gone is neither an error nor recorded.
func TestRemovingAStray(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stray := filepath.Join(dir, "Old Title-320-10.bif")
	if err := os.WriteFile(stray, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := place.OpenRecord(filepath.Join(t.TempDir(), "changes.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Close() }()

	if err := place.Remove(stray, rec, "stray: no video of its name beside it"); err != nil {
		t.Fatal(err)
	}
	if err := place.Remove(stray, rec, "again"); err != nil {
		t.Errorf("removing a file already gone gave %v", err)
	}
	if names := files(t, dir); len(names) != 0 {
		t.Errorf("the folder still holds %q", names)
	}

	raw, err := os.ReadFile(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "\tremoved\t"+stray+"\tstray: ") {
		t.Errorf("the record holds %q", lines)
	}
}

// A share that says a file is not there while its folder lists it, as a Mac's does for a name whose accents are kept apart, has it left and said,
// not taken to be removed. One that says so of a file truly gone is believed.
func TestAShareThatWillNotFindAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	listed := filepath.Join(dir, "Crème-320-10.bif")
	if err := os.WriteFile(listed, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	refuses := func(path string) error { return &os.PathError{Op: "unlink", Path: path, Err: os.ErrNotExist} }

	removed, err := place.RemoveWith(listed, refuses)
	if removed || err == nil || !strings.Contains(err.Error(), "its folder lists it") {
		t.Errorf("a file the share will not find gave %t, %v, want it said that the folder lists it", removed, err)
	}

	removed, err = place.RemoveWith(filepath.Join(dir, "Gone-320-10.bif"), refuses)
	if removed || err != nil {
		t.Errorf("a file truly gone gave %t, %v, want it taken as gone", removed, err)
	}
}
