//go:build !darwin && !linux

package readonly

// forget does nothing where ebb does not know how to drop what the machine holds of a file.
func forget(string) {}
