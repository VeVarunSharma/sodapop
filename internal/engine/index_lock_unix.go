//go:build darwin || linux

package engine

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func lockIndex(ctx context.Context, root *os.Root) (*os.File, error) {
	const name = "sodapop-sessions.lock"
	// Create exclusively, then open existing locks without O_CREATE. This
	// avoids a concurrent non-exclusive creation race on APFS. Root.OpenFile
	// follows in-root symlinks, so also check the existing inode explicitly.
	lock, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	var existing os.FileInfo
	if errors.Is(err, os.ErrExist) {
		existing, err = root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !existing.Mode().IsRegular() {
			return nil, errors.New("Sodapop session lock must not be a symlink or special file")
		}
		lock, err = root.OpenFile(name, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) { return nil, errors.Join(err, lock.Close()) }
	info, err := lock.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fail(errors.New("Sodapop session lock must be a private regular file"))
	}
	if existing != nil && !os.SameFile(existing, info) {
		return fail(errors.New("Sodapop session lock changed while it was being opened"))
	}
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fail(err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail(ctx.Err())
		case <-timer.C:
		}
	}
}
