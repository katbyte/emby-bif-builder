package readonly

import (
	"os"

	"golang.org/x/sys/unix"
)

// shares is the kinds of filesystem a Mac reaches over the network, by the name it gives them.
var shares = map[string]bool{"smbfs": true, "nfs": true, "afpfs": true, "webdav": true}

// forget drops what this Mac holds in memory of the file, when it is on a share, so the next read of it comes from the server. A Mac is not told
// when the server itself changes a file it has read, and goes on handing back parts of it as they were. Dropping nothing is no error: the file is
// read as it is.
func forget(path string) {
	f, err := os.Open(path) //nolint:gosec // the database the user named
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil || !shares[unix.ByteSliceToString(st.Fstypename[:])] {
		return
	}

	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return
	}

	// the file is mapped only to name the pages to drop; nothing of it is read
	m, err := unix.Mmap(int(f.Fd()), 0, int(fi.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return
	}
	defer func() { _ = unix.Munmap(m) }()
	_ = unix.Msync(m, unix.MS_INVALIDATE)
}
