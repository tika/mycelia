package mmap

import (
	"fmt"
	"sync"

	"golang.org/x/exp/mmap"
)

// Reader provides O(1) access to file contents via memory mapping
// Files are mapped on first access and cached for subsequent reads
type Reader struct {
	files map[string]*mappedFile
	mu    sync.RWMutex
}

type mappedFile struct {
	reader *mmap.ReaderAt
	size   int
}

func NewReader() *Reader {
	return &Reader{
		files: make(map[string]*mappedFile),
	}
}

// Slice returns the bytes at [start:end) from the given file in O(1) time
// The file is memory-mapped on first access and cached
func (r *Reader) Slice(path string, start, end uint) ([]byte, error) {
	mf, err := r.getOrMap(path)
	if err != nil {
		return nil, err
	}

	if int(end) > mf.size {
		return nil, fmt.Errorf("end offset %d exceeds file size %d", end, mf.size)
	}
	if start > end {
		return nil, fmt.Errorf("start offset %d > end offset %d", start, end)
	}

	buf := make([]byte, end-start)
	_, err = mf.reader.ReadAt(buf, int64(start))
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}

	return buf, nil
}

// SliceString is like Slice but returns a string
func (r *Reader) SliceString(path string, start, end uint) (string, error) {
	data, err := r.Slice(path, start, end)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *Reader) getOrMap(path string) (*mappedFile, error) {
	r.mu.RLock()
	mf, ok := r.files[path]
	r.mu.RUnlock()

	if ok {
		return mf, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if mf, ok := r.files[path]; ok {
		return mf, nil
	}

	reader, err := mmap.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mmap open failed: %w", err)
	}

	mf = &mappedFile{
		reader: reader,
		size:   reader.Len(),
	}
	r.files[path] = mf

	return mf, nil
}

// Unmaps a file, forcing it to be re-mapped on next access
// Call this when a file has been modified
func (r *Reader) Invalidate(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	mf, ok := r.files[path]
	if !ok {
		return nil
	}

	delete(r.files, path)
	return mf.reader.Close()
}

// Close unmaps all files and releases resources
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for path, mf := range r.files {
		if err := mf.reader.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(r.files, path)
	}

	return firstErr
}
