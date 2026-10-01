package commands

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/manifest"
	"github.com/zgo-cli/zgo/internal/resolve"
)

func RunUpgrade(ctx context.Context, cfg *config.Config, targetPkg string) error {
	manifestStore := manifest.NewStore(filepath.Join(cfg.StateDir, "installed.json"))
	m, err := manifestStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	if len(m.Packages) == 0 {
		fmt.Println("No packages currently installed.")
		return nil
	}

	resolver := resolve.NewResolver("", cfg.CacheDir)

	var targets []manifest.InstalledPackage
	if targetPkg != "" {
		pkg, found := m.Packages[targetPkg]
		if !found {
			return fmt.Errorf("package %q is not installed", targetPkg)
		}
		targets = append(targets, pkg)
	} else {
		for _, pkg := range m.Packages {
			targets = append(targets, pkg)
		}
	}

	for _, pkg := range targets {
		fmt.Printf("Checking updates for %s (current: %s)...\n", pkg.Package, pkg.Version)
		res, err := resolver.Resolve(ctx, pkg.Package, "latest")
		if err != nil {
			fmt.Printf("   ⚠️  Failed to resolve latest version for %s: %v\n", pkg.Package, err)
			continue
		}

		if res.Version == pkg.Version {
			fmt.Printf("   ✅ %s is already up-to-date (%s)\n", pkg.Package, pkg.Version)
			continue
		}

		fmt.Printf("   🔄 Upgrading %s: %s -> %s...\n", pkg.Package, pkg.Version, res.Version)
		err = RunInstall(ctx, cfg, InstallOptions{
			Target: pkg.Package + "@" + res.Version,
		})
		if err != nil {
			fmt.Printf("   ❌ Upgrade failed for %s: %v\n", pkg.Package, err)
		} else {
			fmt.Printf("   ✨ Upgraded %s to %s successfully!\n", pkg.Package, res.Version)
		}
	}

	return nil
}
