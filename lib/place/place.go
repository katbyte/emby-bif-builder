// Package place puts a BIF beside its video so that Emby never reads half of one, removes the BIFs it replaces, and keeps a record of every file
// it writes or removes.
package place

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Record is a list of every BIF ebb wrote, replaced or removed, a line each: when, what was done, the file, and why. It is only ever added to.
type Record struct {
	mu   sync.Mutex
	f    *os.File
	Path string
}

// OpenRecord opens the record at path to add to it, starting it if it is not there. No path keeps no record.
func OpenRecord(path string) (*Record, error) {
	if path == "" {
		return nil, nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the record is the user's own file, named by them
	if err != nil {
		return nil, fmt.Errorf("opening the record of changes: %w", err)
	}

	return &Record{f: f, Path: path}, nil
}

// add writes one line to the record, with tabs between its parts: when, what was done, the file, and why.
func (r *Record) add(what, path, why string) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	line := strings.Join([]string{time.Now().Format(time.RFC3339), what, path, why}, "\t")
	if _, err := r.f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("adding to the record of changes %s: %w", r.Path, err)
	}

	return nil
}

// Close lets the record go.
func (r *Record) Close() error {
	if r == nil {
		return nil
	}

	return r.f.Close()
}

// Change is one file written or removed.
type Change struct {
	// What is wrote, replaced or removed.
	What string
	Path string
}

// Put writes the BIF to target and then removes each of replace, adding each change to the record.
//
// It is written beside the target under a hidden name and renamed into place once whole, so Emby never reads half of one. A target already there is
// only written over when overwrite allows it, so a BIF that appeared since the folder was read is left as it is. Every change that was made is
// returned, with the error from the first that could not be.
func Put(target string, data []byte, overwrite bool, replace []string, rec *Record, why string) ([]Change, error) {
	tmp, err := write(target, data)
	if err != nil {
		return nil, err
	}

	_, err = os.Lstat(target)
	existed := err == nil
	if existed && !overwrite {
		_ = os.Remove(tmp)

		return nil, fmt.Errorf("a BIF appeared at %s since its folder was read, so it was left as it is", target)
	}

	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)

		return nil, fmt.Errorf("putting the BIF in place: %w", err)
	}

	what := "wrote"
	if existed {
		what = "replaced"
	}
	changes := []Change{{What: what, Path: target}}
	if err := rec.add(what, target, why); err != nil {
		return changes, err
	}

	// what landed is checked, since a share can take a write and keep less of it
	if fi, err := os.Stat(target); err != nil || fi.Size() != int64(len(data)) {
		if err == nil {
			err = fmt.Errorf("it is %d bytes, not the %d written", fi.Size(), len(data))
		}

		return changes, fmt.Errorf("checking the BIF written at %s: %w", target, err)
	}

	for _, old := range replace {
		if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return changes, fmt.Errorf("removing %s, which %s replaces: both are there now: %w", old, filepath.Base(target), err)
		}

		changes = append(changes, Change{What: "removed", Path: old})
		if err := rec.add("removed", old, "replaced by "+filepath.Base(target)); err != nil {
			return changes, err
		}
	}

	return changes, nil
}

// Remove removes a file nothing needs and adds its removal to the record. A file already gone is no error, and nothing is recorded for it.
func Remove(path string, rec *Record, why string) error {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	return rec.add("removed", path, why)
}

// write writes the data beside target under a hidden name of its own, and returns that name once all of it is written.
func write(target string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.ebb-tmp")
	if err != nil {
		return "", fmt.Errorf("writing the BIF: %w", err)
	}

	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("writing the BIF: %w", err)
	}

	// a new file is only its writer's to read, and the media server reading it may run as someone else. A share that keeps its own permissions
	// refuses this or ignores it, and the file then has what the share gives every file
	_ = os.Chmod(f.Name(), 0o644) //nolint:gosec // a BIF is read by the media server, which may be another user

	return f.Name(), nil
}
