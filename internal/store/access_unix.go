//go:build unix

package store

import "syscall"

// dirWritable asks whether files may be created in dir (read-only mounts included).
func dirWritable(dir string) error {
	const wOK = 0x2
	return syscall.Access(dir, wOK)
}
