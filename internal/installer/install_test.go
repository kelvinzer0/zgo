package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zgo-cli/zgo/internal/canonical"
	"github.com/zgo-cli/zgo/internal/manifest"
)

func TestAtomicInstaller(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "zgo-install-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cachedDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(cachedDir, 0755)
	srcFile := filepath.Join(cachedDir, "mytool")
	if err := os.WriteFile(srcFile, []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatal(err)
	}

	gobinDir := filepath.Join(tmpDir, "gobin")
	manifestFile := filepath.Join(tmpDir, "installed.json")
	store := manifest.NewStore(manifestFile)
	inst := NewInstaller(store)

	meta := &canonical.Metadata{
		Schema:  2,
		Key:     "testkey123",
		Package: "github.com/example/mytool",
		Version: "v1.0.0",
		Target:  canonical.TargetMeta{GOOS: "linux", GOARCH: "amd64"},
		Binaries: []canonical.BinaryMeta{
			{Name: "mytool", SHA256: "testsha"},
		},
	}

	res, err := inst.InstallBinaries(cachedDir, meta, gobinDir, "linux")
	if err != nil {
		t.Fatalf("InstallBinaries failed: %v", err)
	}

	if len(res.InstalledBinaries) != 1 {
		t.Fatalf("expected 1 installed binary, got %d", len(res.InstalledBinaries))
	}

	installedPath := filepath.Join(gobinDir, "mytool")
	info, err := os.Stat(installedPath)
	if err != nil {
		t.Fatalf("expected binary at %s, got error: %v", installedPath, err)
	}

	// Verify permissions (on Unix systems)
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		t.Fatalf("expected file to be executable, got mode: %v", info.Mode())
	}

	// Verify manifest
	pkg, found, err := store.Get("github.com/example/mytool")
	if err != nil || !found {
		t.Fatalf("expected package in manifest: found=%v, err=%v", found, err)
	}
	if pkg.Version != "v1.0.0" {
		t.Fatalf("expected v1.0.0, got %s", pkg.Version)
	}
}
