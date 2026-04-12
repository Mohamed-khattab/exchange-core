//go:build linux

package wal

import (
	"os"
	"syscall"
)

func fdatasyncPlatform(f *os.File) error {
	return syscall.Fdatasync(int(f.Fd()))
}
