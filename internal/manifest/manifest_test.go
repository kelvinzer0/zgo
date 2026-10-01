package manifest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "zgo-manifest-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	manifestPath := filepath.Join(tmpDir, "installed.json")
	store := NewStore(manifestPath)

	// Check initially empty
	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(m.Packages) != 0 {
		t.Fatalf("expected 0 packages, got %d", len(m.Packages))
	}

	// Record an installation
	pkg := InstalledPackage{
		Package: "github.com/charmbracelet/glow",
		Module:  "github.com/charmbracelet/glow",
		Version: "v1.5.0",
		Commit:  "1234567890abcdef",
		Key:     "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		Target:  TargetMeta{GOOS: "linux", GOARCH: "amd64"},
		Binaries: []InstalledBinary{
			{Name: "glow", Path: "/home/user/go/bin/glow", SHA256: "abc123sha"},
		},
	}

	if err := store.RecordInstall(pkg); err != nil {
		t.Fatalf("RecordInstall failed: %v", err)
	}

	// Verify it can be retrieved
	fetched, found, err := store.Get("github.com/charmbracelet/glow")
	if err != nil || !found {
		t.Fatalf("Get failed: found=%v, err=%v", found, err)
	}
	if fetched.Version != "v1.5.0" || len(fetched.Binaries) != 1 {
		t.Fatalf("unexpected fetched package: %+v", fetched)
	}

	// Update it with a new version
	pkg.Version = "v1.5.1"
	time.Sleep(10 * time.Millisecond)
	if err := store.RecordInstall(pkg); err != nil {
		t.Fatalf("RecordInstall update failed: %v", err)
	}

	fetched2, found, err := store.Get("github.com/charmbracelet/glow")
	if err != nil || !found || fetched2.Version != "v1.5.1" {
		t.Fatalf("update check failed: %+v", fetched2)
	}
	if !fetched2.UpdatedAt.After(fetched2.InstalledAt) {
		t.Fatalf("expected UpdatedAt to be after InstalledAt")
	}

	// Remove it
	removed, err := store.Remove("github.com/charmbracelet/glow")
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if removed.Package != "github.com/charmbracelet/glow" {
		t.Fatalf("unexpected removed package: %+v", removed)
	}

	// Verify it's gone
	_, foundAfter, err := store.Get("github.com/charmbracelet/glow")
	if err != nil || foundAfter {
		t.Fatalf("expected package to be gone")
	}
}
