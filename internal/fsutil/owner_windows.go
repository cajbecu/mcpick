//go:build windows

package fsutil

import "os"

// ownedByMe: the temp directory is per-user on Windows already.
func ownedByMe(os.FileInfo) bool { return true }
