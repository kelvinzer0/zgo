package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/manifest"
)

func RunUninstall(cfg *config.Config, pkgName string) error {
	if pkgName == "" {
		return fmt.Errorf("missing package name. Usage: zgo uninstall <pkg>")
	}

	manifestStore := manifest.NewStore(filepath.Join(cfg.StateDir, "installed.json"))
	pkg, found, err := manifestStore.Get(pkgName)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("package %q is not installed", pkgName)
	}

	var removedFiles []string
	var removeErrors []string

	for _, b := range pkg.Binaries {
		if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
			removeErrors = append(removeErrors, fmt.Sprintf("%s (%v)", b.Path, err))
		} else {
			removedFiles = append(removedFiles, b.Path)
		}
	}

	if _, err := manifestStore.Remove(pkgName); err != nil {
		return fmt.Errorf("failed to update manifest: %w", err)
	}

	fmt.Printf("🗑️  Uninstalled %s\n", pkgName)
	for _, f := range removedFiles {
		fmt.Printf("   - Removed: %s\n", f)
	}
	for _, e := range removeErrors {
		fmt.Fprintf(os.Stderr, "   ⚠️  Could not remove: %s\n", e)
	}

	return nil
}
