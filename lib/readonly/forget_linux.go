package readonly

import (
	"os"

	"golang.org/x/sys/unix"
)

// shares is the kinds of filesystem Linux reaches over the network, by the number it gives them: SMB, SMB2 and NFS.
var shares = map[uint32]bool{0xff534d42: true, 0xfe534d42: true, 0x517b: true, 0x6969: true}

// forget drops what this machine holds in memory of the file, when it is on a share, so the next read of it comes from the server. A file on the
// machine's own disk is left alone: it cannot be stale, and its pages may be the server's own. Dropping nothing is no error: the file is read as it is.
func forget(path string) {
	f, err := os.Open(path) //nolint:gosec // the database the user named
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil || !shares[uint32(st.Type)] { //nolint:gosec // a file descriptor fits an int, and the filesystem's number in 32 bits
		return
	}
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}
