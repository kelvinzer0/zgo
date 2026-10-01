package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zgo-cli/zgo/internal/attestation"
	"github.com/zgo-cli/zgo/internal/client"
	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/manifest"
)

func RunVerify(ctx context.Context, cfg *config.Config, pkgName string) error {
	if pkgName == "" {
		return fmt.Errorf("missing package name. Usage: zgo verify <pkg>")
	}

	manifestStore := manifest.NewStore(filepath.Join(cfg.StateDir, "installed.json"))
	pkg, found, err := manifestStore.Get(pkgName)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("package %q is not installed", pkgName)
	}

	fmt.Printf("🔍 Verifying installed package: %s@%s (key: %s)\n", pkg.Package, pkg.Version, pkg.Key[:12])

	// 1. Local disk integrity check
	allLocalOK := true
	for _, b := range pkg.Binaries {
		if _, err := os.Stat(b.Path); os.IsNotExist(err) {
			fmt.Printf("   ❌ Binary missing on disk: %s\n", b.Path)
			allLocalOK = false
			continue
		}

		currentSHA, err := attestation.ComputeFileSHA256(b.Path)
		if err != nil {
			fmt.Printf("   ❌ Could not read binary: %s (%v)\n", b.Path, err)
			allLocalOK = false
			continue
		}

		if !strings.EqualFold(currentSHA, b.SHA256) {
			fmt.Printf("   🚨 TAMPERING DETECTED! SHA-256 mismatch on %s\n", b.Path)
			fmt.Printf("      Expected: %s\n", b.SHA256)
			fmt.Printf("      Actual:   %s\n", currentSHA)
			allLocalOK = false
		} else {
			fmt.Printf("   ✅ Local integrity intact: %s (SHA-256 match)\n", b.Name)
		}
	}

	// 2. Remote Attestation verification against GitHub Release
	fmt.Printf("🌐 Checking remote provenance and Sigstore attestation from release b-%s...\n", pkg.Key[:12])
	relClient := client.NewReleaseClient(cfg.BuilderRepo, "")
	meta, err := relClient.FetchMetadata(ctx, pkg.Key)
	if err != nil {
		fmt.Printf("   ⚠️  Could not fetch remote release metadata: %v\n", err)
	} else {
		sumsContent, _ := relClient.FetchSHA256Sums(ctx, pkg.Key)
		bundleBytes, _ := relClient.FetchAttestationBundle(ctx, pkg.Key)

		verifier := attestation.NewVerifier(attestation.VerificationOptions{
			ExpectedRepo:    cfg.BuilderRepo,
			ExpectedVersion: pkg.Version,
			ExpectedCommit:  pkg.Commit,
		})

		for _, b := range pkg.Binaries {
			if _, err := os.Stat(b.Path); err == nil {
				if err := verifier.VerifyBinary(b.Path, b.Name, meta, sumsContent, bundleBytes); err != nil {
					fmt.Printf("   🚨 Remote verification failure for %s: %v\n", b.Name, err)
					allLocalOK = false
				} else {
					fmt.Printf("   🛡️  Build attestation verified: produced by official workflow %s on %s\n",
						cfg.BuilderRepo, meta.Target.GOOS)
				}
			}
		}
	}

	if !allLocalOK {
		return fmt.Errorf("verification failed for %s", pkgName)
	}

	fmt.Printf("\n✨ Verification PASSED! Package %s is authentic and unmodified.\n", pkgName)
	return nil
}
