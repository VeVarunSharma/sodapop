//go:build windows

package engine

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
	"golang.org/x/sys/windows"
)

func lockIndex(ctx context.Context, root *os.Root) (*os.File, error) {
	const name = "sodapop-sessions.lock"
	lock, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	created := err == nil
	var existing os.FileInfo
	if errors.Is(err, os.ErrExist) {
		existing, err = root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !existing.Mode().IsRegular() {
			return nil, errors.New("Sodapop session lock must not be a link or special file")
		}
		lock, err = root.OpenFile(name, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) { return nil, errors.Join(err, lock.Close()) }
	if created {
		if err := securefs.ProtectFile(lock); err != nil {
			closeErr := lock.Close()
			removeErr := root.Remove(name)
			return nil, errors.Join(err, closeErr, removeErr)
		}
	}
	info, err := lock.Stat()
	if err != nil {
		return fail(err)
	}
	if existing != nil && !os.SameFile(existing, info) {
		return fail(errors.New("Sodapop session lock changed while it was being opened"))
	}
	private, err := securefs.IsPrivateRegularFile(lock)
	if err != nil {
		return fail(err)
	}
	if !private {
		return fail(errors.New("Sodapop session lock must be a private regular file"))
	}
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		err := tryLockFile(lock)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
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

func tryLockFile(file *os.File) error {
	connection, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var callErr error
	if err := connection.Control(func(handle uintptr) {
		var overlapped windows.Overlapped
		callErr = windows.LockFileEx(
			windows.Handle(handle),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			&overlapped,
		)
	}); err != nil {
		return err
	}
	return callErr
}
