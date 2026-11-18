package filesystem

import (
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Store struct{ root *os.Root }

func New(directory string) (*Store, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Store{root}, nil
}
func (s *Store) Create(key string) (*os.File, error) {
	return s.root.OpenFile(key, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
}
func (s *Store) Open(key string) (*os.File, error) { return s.root.Open(key) }
func (s *Store) Remove(key string) error           { return s.root.Remove(key) }
func (s *Store) Close() error                      { return s.root.Close() }
func (s *Store) SyncDirectory() error {
	f, err := s.root.Open(".")
	if err != nil {
		return err
	}
	err = f.Sync()
	f.Close()
	return err
}
func (s *Store) Inspect(file *os.File, name string, size int64) (string, error) {
	return fileType(file, name, size)
}
func fileType(file *os.File, filename string, size int64) (string, error) {
	var header [512]byte
	n, err := file.ReadAt(header[:], 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	detected := strings.Split(http.DetectContentType(header[:n]), ";")[0]
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case ext == ".pdf" && detected == "application/pdf":
		return detected, nil
	case (ext == ".jpg" || ext == ".jpeg") && detected == "image/jpeg", ext == ".png" && detected == "image/png":
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		config, _, err := image.DecodeConfig(file)
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 25_000_000/config.Height {
			return "", errors.New("invalid image")
		}
		return detected, nil
	case ext == ".txt" && detected == "text/plain":
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(file, size))
		if err != nil || !utf8.Valid(data) {
			return "", errors.New("invalid text")
		}
		for _, char := range string(data) {
			if unicode.IsControl(char) && char != '\r' && char != '\n' && char != '\t' {
				return "", errors.New("invalid text")
			}
		}
		return detected, nil
	default:
		return "", errors.New("unsupported file")
	}
}
