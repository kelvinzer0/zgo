package cache

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zgo-cli/zgo/internal/canonical"
)

// Store manages the local content cache for ZGo binaries and metadata.
type Store struct {
	baseDir string
}

// DefaultCacheDir returns os.UserCacheDir()/zgo
func DefaultCacheDir() string {
	cacheRoot, err := os.UserCacheDir()
	if err == nil && cacheRoot != "" {
		return filepath.Join(cacheRoot, "zgo")
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".cache", "zgo")
	}
	return filepath.Join(os.TempDir(), "zgo-cache")
}

func NewStore(dir string) *Store {
	if dir == "" {
		dir = DefaultCacheDir()
	}
	return &Store{baseDir: dir}
}

func (s *Store) KeyDir(key string) string {
	return filepath.Join(s.baseDir, "keys", key)
}

// HasKey checks if the metadata for this key is cached locally
func (s *Store) HasKey(key string) bool {
	metaPath := filepath.Join(s.KeyDir(key), "metadata.json")
	info, err := os.Stat(metaPath)
	return err == nil && !info.IsDir()
}

// GetMetadata loads the cached metadata.json for key
func (s *Store) GetMetadata(key string) (*canonical.Metadata, error) {
	metaPath := filepath.Join(s.KeyDir(key), "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta canonical.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse cached metadata: %w", err)
	}
	return &meta, nil
}

// SaveArtifact saves a binary file and its metadata to the cache directory
func (s *Store) SaveArtifact(key string, filename string, r io.Reader) (string, error) {
	kDir := s.KeyDir(key)
	if err := os.MkdirAll(kDir, 0755); err != nil {
		return "", err
	}

	destPath := filepath.Join(kDir, filename)
	tmpPath := fmt.Sprintf("%s.tmp", destPath)

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	_ = f.Close()

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	return destPath, nil
}

// SaveMetadata saves metadata.json in the key directory
func (s *Store) SaveMetadata(key string, meta *canonical.Metadata) error {
	kDir := s.KeyDir(key)
	if err := os.MkdirAll(kDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	metaPath := filepath.Join(kDir, "metadata.json")
	return os.WriteFile(metaPath, data, 0644)
}

// GetBinaryPath returns the path to a cached binary if it exists
func (s *Store) GetBinaryPath(key string, binaryName string) (string, bool) {
	p := filepath.Join(s.KeyDir(key), binaryName)
	info, err := os.Stat(p)
	if err == nil && !info.IsDir() {
		return p, true
	}
	return "", false
}

// Clean removes all cached artifacts
func (s *Store) Clean() error {
	return os.RemoveAll(s.baseDir)
}

// TotalSize calculates total size of the cache in bytes
func (s *Store) TotalSize() (int64, error) {
	var total int64
	err := filepath.Walk(s.baseDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
