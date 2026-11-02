package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Change is one file an install or uninstall would write, replace or remove.
// Old is what the file holds now (nil when it doesn't exist); New is what it
// should hold. A plan is a list of changes that has made no change yet.
type Change struct {
	Path   string
	Old    []byte
	New    []byte
	Mode   os.FileMode
	Backup bool // copy Old to Path+".bak-hive" before writing or removing
	Remove bool // delete Path instead of writing New
}

// Current returns a file's contents and whether it exists, for a plan.
func Current(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Apply carries out changes: the backups first, then the writes and removals.
// It leaves a file's existing permissions alone, as an install did before.
func Apply(changes []Change) error {
	for _, c := range changes {
		if c.Backup {
			if _, err := os.Stat(c.Path); err == nil {
				if err := os.WriteFile(c.Path+".bak-hive", c.Old, 0o600); err != nil {
					return fmt.Errorf("backup: %w", err)
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if c.Remove {
			if err := os.Remove(c.Path); err != nil {
				return err
			}
			continue
		}
		if err := WriteFileAtomic(c.Path, c.New, c.Mode); err != nil {
			return err
		}
	}
	return nil
}
