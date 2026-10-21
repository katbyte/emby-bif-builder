package inventory

// FolderWith and UnreachableWith let the tests stand in for a share that lists a file it cannot open.
var (
	FolderWith      = readFolder
	UnreachableWith = unreachable
)
