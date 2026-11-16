package repo

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultMaxLogSize = 10 * 1024 * 1024
	defaultMaxBackups = 3
)

// RotatingWriter appends to a file and rotates it when it reaches maxSize.
type RotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	maxBackups int

	file *os.File
	size int64
}

// NewRotatingWriter opens path for appending, rotating once maxSize is exceeded
// and keeping up to maxBackups old files.
func NewRotatingWriter(path string, maxSize int64, maxBackups int) (*RotatingWriter, error) {
	if path == "" {
		return nil, errors.New("log path is empty")
	}
	if maxSize <= 0 {
		maxSize = defaultMaxLogSize
	}
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	writer := &RotatingWriter{
		path:       path,
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *RotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

// Write appends p, rotating first when it would exceed the size limit.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, errors.New("rotating writer is closed")
	}
	if w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	for index := w.maxBackups - 1; index >= 1; index-- {
		from := fmt.Sprintf("%s.%d", w.path, index)
		to := fmt.Sprintf("%s.%d", w.path, index+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return w.open()
}

// Close flushes and closes the current log file. It is safe to call more than once.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// SetupLogging points the standard logger at a rotating file and returns a closer.
func SetupLogging(path string) (io.Closer, error) {
	writer, err := NewRotatingWriter(path, defaultMaxLogSize, defaultMaxBackups)
	if err != nil {
		return nil, err
	}
	log.SetOutput(writer)
	log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds)
	return writer, nil
}
