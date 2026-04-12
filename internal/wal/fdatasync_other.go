//go:build !linux

package wal

import "os"

func fdatasyncPlatform(f *os.File) error {
	return f.Sync()
}
