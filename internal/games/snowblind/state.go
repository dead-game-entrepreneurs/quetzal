package snowblind

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Save tenant state before publishing it in memory. Social and stat stores
// share private permissions and the same atomic replacement protocol.
func saveState(path, prefix string, value any, limit int) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > limit {
		return errors.New("Snowblind state capacity exceeded")
	}
	f, err := os.CreateTemp(filepath.Dir(path), prefix)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
