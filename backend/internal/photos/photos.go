// Package photos keeps occurrence photos as files on disk. The database only
// stores their names, so the folder can move to object storage later.
package photos

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

var ErrNotFound = errors.New("Foto não encontrada.")

// Names are "<occurrence uuid>.<jpg|png>"; anything else is rejected before
// touching the filesystem.
var namePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|png)$`)

type Dir struct{ root string }

func Open(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &Dir{root: root}, nil
}

func (d *Dir) path(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", ErrNotFound
	}
	return filepath.Join(d.root, name), nil
}

// Save writes through a temporary file so a crash never leaves half a photo.
func (d *Dir) Save(name string, data []byte) error {
	path, err := d.path(name)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.root, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (d *Dir) Read(name string) ([]byte, error) {
	path, err := d.path(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

func (d *Dir) Remove(name string) error {
	path, err := d.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ContentType maps a stored name to its media type.
func ContentType(name string) string {
	if filepath.Ext(name) == ".png" {
		return "image/png"
	}
	return "image/jpeg"
}
