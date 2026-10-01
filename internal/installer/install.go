package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zgo-cli/zgo/internal/attestation"
	"github.com/zgo-cli/zgo/internal/canonical"
	"github.com/zgo-cli/zgo/internal/env"
	"github.com/zgo-cli/zgo/internal/manifest"
)

type InstallResult struct {
	InstalledBinaries []manifest.InstalledBinary
	GOBIN             string
	NotInPATHWarning  bool
}

type Installer struct {
	manifestStore *manifest.Store
}

func NewInstaller(store *manifest.Store) *Installer {
	return &Installer{manifestStore: store}
}

// InstallBinaries copies verified binaries into GOBIN atomically and updates the manifest
func (inst *Installer) InstallBinaries(
	cachedKeyDir string,
	meta *canonical.Metadata,
	gobin string,
	targetGOOS string,
) (*InstallResult, error) {
	if gobin == "" {
		return nil, fmt.Errorf("GOBIN path cannot be empty")
	}

	if err := os.MkdirAll(gobin, 0755); err != nil {
		return nil, fmt.Errorf("failed to create GOBIN directory %s: %w", gobin, err)
	}

	isWindows := (strings.ToLower(targetGOOS) == "windows")
	var installedBinaries []manifest.InstalledBinary

	for _, b := range meta.Binaries {
		sourceFilename := b.Name
		destFilename := b.Name
		if isWindows && !strings.HasSuffix(destFilename, ".exe") {
			destFilename += ".exe"
			if _, err := os.Stat(filepath.Join(cachedKeyDir, sourceFilename)); os.IsNotExist(err) {
				sourceFilename += ".exe"
			}
		}

		sourcePath := filepath.Join(cachedKeyDir, sourceFilename)
		if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
			return nil, fmt.Errorf("source binary %s does not exist in cache: %s", sourceFilename, sourcePath)
		}

		destPath := filepath.Join(gobin, destFilename)
		tmpDestPath := filepath.Join(gobin, fmt.Sprintf(".tmp-%s-%d", destFilename, time.Now().UnixNano()))

		if err := copyExecutableFile(sourcePath, tmpDestPath); err != nil {
			_ = os.Remove(tmpDestPath)
			return nil, fmt.Errorf("failed to write binary %s: %w", destFilename, err)
		}

		// Atomic rename
		if err := os.Rename(tmpDestPath, destPath); err != nil {
			_ = os.Remove(tmpDestPath)
			return nil, fmt.Errorf("failed to atomically replace binary %s: %w", destPath, err)
		}

		// Calculate SHA256 of the installed binary for record
		sha, _ := attestation.ComputeFileSHA256(destPath)
		installedBinaries = append(installedBinaries, manifest.InstalledBinary{
			Name:   destFilename,
			Path:   destPath,
			SHA256: sha,
		})
	}

	// Update local manifest
	installedPkg := manifest.InstalledPackage{
		Package:     meta.Package,
		Version:     meta.Version,
		Commit:      meta.Commit,
		Key:         meta.Key,
		Target:      manifest.TargetMeta{GOOS: meta.Target.GOOS, GOARCH: meta.Target.GOARCH},
		Binaries:    installedBinaries,
		InstalledAt: time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := inst.manifestStore.RecordInstall(installedPkg); err != nil {
		return nil, fmt.Errorf("failed to record installation in manifest: %w", err)
	}

	notInPath := !env.CheckGOBINInPATH(gobin)

	return &InstallResult{
		InstalledBinaries: installedBinaries,
		GOBIN:             gobin,
		NotInPATHWarning:  notInPath,
	}, nil
}

// copyExecutableFile copies file contents and sets 0755 permissions
func copyExecutableFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}

	return out.Chmod(0755)
}
