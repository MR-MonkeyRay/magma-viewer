package web

import "syscall"

// diskSpace reports the file system holding dir, or nil when unknown.
func diskSpace(dir string) *disk {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return nil
	}
	return &disk{Free: int64(st.Bavail) * int64(st.Bsize), Total: int64(st.Blocks) * int64(st.Bsize)} //nolint:unconvert // Bsize is int32 on 32-bit
}
