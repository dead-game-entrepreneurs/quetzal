// Package state owns private persisted keys and process-wide directory locks.
package state

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func Open(directory string) (*os.File, quazalcrypto.Key, error) {
	lock, err := Lock(directory)
	if err != nil {
		return nil, quazalcrypto.Key{}, err
	}
	key, err := PrincipalKey(directory)
	if err != nil {
		lock.Close()
		return nil, quazalcrypto.Key{}, err
	}
	return lock, key, nil
}

// The service principal key is generated once. Replacing it invalidates issued
// destination tickets, so a server restart must load the same private value.
func PrincipalKey(directory string) (quazalcrypto.Key, error) {
	return LoadKey(directory, "principal.key")
}

func LoadKey(directory, name string) (quazalcrypto.Key, error) {
	var key quazalcrypto.Key
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return key, errors.New("key name must be a single filename")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return key, err
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return key, fmt.Errorf("%s must be a regular private file (0600)", path)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != len(key) {
			return key, fmt.Errorf("%s must contain exactly %d bytes", path, len(key))
		}
		copy(key[:], data)
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return key, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return LoadKey(directory, name)
	}
	if err != nil {
		return quazalcrypto.Key{}, err
	}
	if _, err = file.Write(key[:]); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return quazalcrypto.Key{}, err
	}
	return key, nil
}

// Only one process may write an account directory. Port separation alone does
// not make two independently loaded PID/account stores safe to mutate.
func Lock(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("state directory already in use or cannot be locked: %w", err)
	}
	return file, nil
}
