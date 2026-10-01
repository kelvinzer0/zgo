package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// InstalledBinary records an installed executable binary
type InstalledBinary struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// TargetMeta describes the target OS/arch
type TargetMeta struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

// InstalledPackage represents a package installed by ZGo
type InstalledPackage struct {
	Package     string            `json:"package"`
	Module      string            `json:"module"`
	Version     string            `json:"version"`
	Commit      string            `json:"commit"`
	Key         string            `json:"key"`
	Target      TargetMeta        `json:"target"`
	Binaries    []InstalledBinary `json:"binaries"`
	InstalledAt time.Time         `json:"installed_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Manifest holds the collection of installed packages
type Manifest struct {
	Packages map[string]InstalledPackage `json:"packages"`
}

type Store struct {
	filePath string
	mu       sync.Mutex
}

// UserStateDir returns the default root directory for state files
// following Linux XDG (~/.local/state), macOS, and Windows conventions.
func UserStateDir() (string, error) {
	if xdgState := os.Getenv("XDG_STATE_HOME"); xdgState != "" {
		return xdgState, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Windows: %LocalAppData%
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return localAppData, nil
	}

	// Linux / macOS standard state directory
	return filepath.Join(home, ".local", "state"), nil
}

// DefaultManifestPath resolves the manifest path according to UserStateDir()/zgo/installed.json
func DefaultManifestPath() string {
	stateDir, err := UserStateDir()
	if err == nil && stateDir != "" {
		return filepath.Join(stateDir, "zgo", "installed.json")
	}

	// Fallback to temporary directory if state dir cannot be found
	return filepath.Join(os.TempDir(), "zgo-state", "installed.json")
}

func NewStore(path string) *Store {
	if path == "" {
		path = DefaultManifestPath()
	}
	return &Store{filePath: path}
}

// Load reads the manifest from disk. If the file does not exist, an empty manifest is returned.
func (s *Store) Load() (*Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Packages: make(map[string]InstalledPackage)}, nil
		}
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}
	if m.Packages == nil {
		m.Packages = make(map[string]InstalledPackage)
	}
	return &m, nil
}

// Save atomically writes the manifest to disk.
func (s *Store) Save(m *Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create manifest directory: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", s.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary manifest file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to commit manifest file: %w", err)
	}

	return nil
}

// RecordInstall adds or updates an installed package in the manifest atomically.
func (s *Store) RecordInstall(pkg InstalledPackage) error {
	m, err := s.Load()
	if err != nil {
		return err
	}

	if existing, found := m.Packages[pkg.Package]; found {
		pkg.InstalledAt = existing.InstalledAt
		pkg.UpdatedAt = time.Now()
	} else {
		pkg.InstalledAt = time.Now()
		pkg.UpdatedAt = pkg.InstalledAt
	}

	m.Packages[pkg.Package] = pkg
	return s.Save(m)
}

// Remove deletes an installed package from the manifest.
func (s *Store) Remove(pkgName string) (*InstalledPackage, error) {
	m, err := s.Load()
	if err != nil {
		return nil, err
	}

	pkg, found := m.Packages[pkgName]
	if !found {
		return nil, fmt.Errorf("package %q is not installed", pkgName)
	}

	delete(m.Packages, pkgName)
	if err := s.Save(m); err != nil {
		return nil, err
	}

	return &pkg, nil
}

// Get returns the installed package if found.
func (s *Store) Get(pkgName string) (*InstalledPackage, bool, error) {
	m, err := s.Load()
	if err != nil {
		return nil, false, err
	}

	pkg, found := m.Packages[pkgName]
	return &pkg, found, nil
}
