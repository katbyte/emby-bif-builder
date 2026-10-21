// Package paths turns the paths Emby and Jellyfin know their videos by into the paths ebb finds them at, and back, for when ebb sees the media
// somewhere else than the servers do: over a share on another machine, say.
package paths

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Map is a list of server folders and where ebb finds each. A path in none of them is where the servers see it.
type Map []pair

// pair is one server folder and where ebb finds it.
type pair struct {
	server string
	local  string
}

// Parse reads a map written as server=local,server=local: /mnt/video/tv=/Volumes/video/tv. A folder can be named once.
func Parse(s string) (Map, error) {
	var m Map
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}

		server, local, ok := strings.Cut(part, "=")
		server, local = clean(server), clean(local)
		if !ok || server == "" || local == "" {
			return nil, fmt.Errorf("%q is not a server folder and where it is found here, as /mnt/video=/Volumes/video", part)
		}
		if server == "/" || local == "/" {
			return nil, fmt.Errorf("%q maps the whole disk: name the folders the videos are in", part)
		}
		if slices.ContainsFunc(m, func(p pair) bool { return p.server == server }) {
			return nil, fmt.Errorf("%s is given twice", server)
		}
		m = append(m, pair{server: server, local: local})
	}

	return m, nil
}

// Local is where ebb finds what the servers call path: under the deepest server folder that holds it, or the path itself when none does.
func (m Map) Local(path string) string {
	if p, ok := deepest(m, path, func(p pair) string { return p.server }); ok {
		return p.local + path[len(p.server):]
	}

	return path
}

// Server is what the servers call a path ebb was given, the other way round from Local.
func (m Map) Server(path string) string {
	path = filepath.ToSlash(path)
	if p, ok := deepest(m, path, func(p pair) string { return p.local }); ok {
		return p.server + path[len(p.local):]
	}

	return path
}

// String is the map as Parse reads it.
func (m Map) String() string {
	parts := make([]string, 0, len(m))
	for _, p := range m {
		parts = append(parts, p.server+"="+p.local)
	}

	return strings.Join(parts, ",")
}

// deepest is the pair whose folder, as side gives it, holds path and is the deepest in.
func deepest(m Map, path string, side func(pair) string) (pair, bool) {
	var (
		best  pair
		found bool
	)
	for _, p := range m {
		f := side(p)
		if (path == f || strings.HasPrefix(path, f+"/")) && (!found || len(f) > len(side(best))) {
			best, found = p, true
		}
	}

	return best, found
}

// clean is a folder without the spaces around it or a slash after it.
func clean(s string) string {
	s = filepath.ToSlash(strings.TrimSpace(s))
	if len(s) > 1 {
		s = strings.TrimRight(s, "/")
	}

	return s
}
