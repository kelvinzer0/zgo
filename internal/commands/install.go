package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zgo-cli/zgo/internal/attestation"
	"github.com/zgo-cli/zgo/internal/cache"
	"github.com/zgo-cli/zgo/internal/canonical"
	"github.com/zgo-cli/zgo/internal/client"
	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/env"
	"github.com/zgo-cli/zgo/internal/installer"
	"github.com/zgo-cli/zgo/internal/manifest"
	"github.com/zgo-cli/zgo/internal/resolve"
)

type InstallOptions struct {
	Target          string // pkg[@ver]
	GOOS            string
	GOARCH          string
	CGO             *bool
	Tags            string
	Timeout         time.Duration
	NoCache         bool
	SelfHosted      bool
	SkipAttestation bool // for tests/offline
}

func RunInstall(ctx context.Context, cfg *config.Config, opts InstallOptions) error {
	startTime := time.Now()

	if strings.TrimSpace(opts.Target) == "" {
		return fmt.Errorf("missing package argument. Usage: zgo install <pkg>[@<version>]")
	}

	rawPkg, rawVer := resolve.ParseTarget(opts.Target)

	// 1. Snapshot env
	envResolver := env.NewEnvResolver()
	snap, err := envResolver.ResolveSnapshot(env.FlagsOption{
		GOOS:       opts.GOOS,
		GOARCH:     opts.GOARCH,
		CGO:        opts.CGO,
		Tags:       opts.Tags,
		SelfHosted: opts.SelfHosted,
	})
	if err != nil {
		return fmt.Errorf("environment validation error: %w", err)
	}

	// Print warnings if any
	for _, w := range snap.Warnings {
		fmt.Fprintf(os.Stderr, "⚠️  [Warning] %s\n", w)
	}

	cacheStore := cache.NewStore(cfg.CacheDir)
	manifestStore := manifest.NewStore(filepath.Join(cfg.StateDir, "installed.json"))
	inst := installer.NewInstaller(manifestStore)

	// 2. Resolve module, concrete version, and commit via proxy
	fmt.Printf("🔍 Resolving %s@%s...\n", rawPkg, rawVer)
	resolver := resolve.NewResolver(snap.GOPROXY, cfg.CacheDir)
	resolved, err := resolver.Resolve(ctx, rawPkg, rawVer)
	if err != nil {
		return fmt.Errorf("resolution failed: %w", err)
	}

	commitDisplay := resolved.Commit
	if len(commitDisplay) > 8 {
		commitDisplay = commitDisplay[:8]
	}
	if commitDisplay == "" {
		commitDisplay = "unknown"
	}
	fmt.Printf("📦 Resolved to %s@%s (commit: %s)\n", resolved.Package, resolved.Version, commitDisplay)

	// 3. Build Canonical Request & Cache Key
	canReq := &canonical.CanonicalRequest{
		BuilderSchema: canonical.CurrentBuilderSchema,
		Module:        resolved.Module,
		Version:       resolved.Version,
		Commit:        resolved.Commit,
		Package:       resolved.Package,
		GOOS:          snap.GOOS,
		GOARCH:        snap.GOARCH,
		GOARM:         snap.GOARM,
		GOAMD64:       snap.GOAMD64,
		GO386:         snap.GO386,
		GOMIPS:        snap.GOMIPS,
		GOMIPS64:      snap.GOMIPS64,
		GOPPC64:       snap.GOPPC64,
		CGOEnabled:    snap.CGOEnabled,
		GOFLAGS:       snap.GOFLAGS,
		Toolchain:     snap.Toolchain,
	}

	key, err := canReq.ComputeKey()
	if err != nil {
		return fmt.Errorf("failed to compute cache key: %w", err)
	}
	fmt.Printf("🔑 Cache Key: %s (target: %s/%s)\n", key[:12], snap.GOOS, snap.GOARCH)

	// 4. Check Local Cache (HIT -> Install directly)
	if !opts.NoCache && cacheStore.HasKey(key) {
		meta, err := cacheStore.GetMetadata(key)
		if err == nil && meta != nil {
			fmt.Printf("⚡ Local cache HIT! Installing from cache...\n")
			res, err := inst.InstallBinaries(cacheStore.KeyDir(key), meta, snap.GOBIN, snap.GOOS)
			if err == nil {
				printInstallSuccess(res, time.Since(startTime))
				return nil
			}
		}
	}

	// 5. Check Global Cache (GitHub Releases b-<key>)
	relClient := client.NewReleaseClient(cfg.BuilderRepo, "")
	fmt.Printf("🌐 Checking global release cache (b-%s)...\n", key[:12])
	hasRelease, err := relClient.CheckReleaseExists(ctx, key)
	if err == nil && hasRelease {
		fmt.Printf("⚡ Global cache HIT! (< 3s)\n")
		return downloadVerifyAndInstall(ctx, relClient, key, canReq, cacheStore, inst, snap, cfg, startTime, opts.SkipAttestation)
	}

	// 6. Cache MISS -> Dispatch Build
	fmt.Printf("⏳ Cache MISS. Initiating remote build...\n")
	if opts.SelfHosted {
		selfHostedClient := client.NewSelfHostedClient(cfg.BuilderRepo, cfg.GitHubToken)
		fmt.Printf("🚀 Dispatching workflow to self-hosted repository %s...\n", cfg.BuilderRepo)
		if err := selfHostedClient.DispatchBuild(ctx, canReq, key); err != nil {
			return fmt.Errorf("self-hosted build dispatch failed: %w", err)
		}
	} else {
		brokerClient := client.NewBrokerClient(cfg.BrokerURL)
		fmt.Printf("📡 Dispatching build via broker single-flight...\n")
		buildResp, err := brokerClient.RequestBuild(ctx, canReq)
		if err != nil {
			return fmt.Errorf("broker build dispatch failed: %w", err)
		}
		if buildResp.RunURL != "" {
			fmt.Printf("🔗 Run URL: %s\n", buildResp.RunURL)
		}

		// Wait via SSE / fallback polling
		fmt.Printf("⏳ Waiting for build completion (streaming status via SSE)...\n")
		timeout := opts.Timeout
		if timeout == 0 {
			timeout = cfg.DefaultTTL
		}
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		var lastStatus, lastMessage string
		_, err = brokerClient.WaitForCompletion(waitCtx, key, func(status *client.BuildStatus) {
			if status.Status == lastStatus && status.Message == lastMessage {
				return
			}
			lastStatus = status.Status
			lastMessage = status.Message

			if status.Message != "" {
				fmt.Printf("   » [%s] %s\n", status.Status, status.Message)
			} else {
				fmt.Printf("   » Status: %s\n", status.Status)
			}
		})
		if err != nil {
			return fmt.Errorf("remote build failed or timed out: %w", err)
		}
	}

	// 7. Post-build download, verify, and install
	return downloadVerifyAndInstall(ctx, relClient, key, canReq, cacheStore, inst, snap, cfg, startTime, opts.SkipAttestation)
}

func downloadVerifyAndInstall(
	ctx context.Context,
	relClient *client.ReleaseClient,
	key string,
	req *canonical.CanonicalRequest,
	cacheStore *cache.Store,
	inst *installer.Installer,
	snap *env.Snapshot,
	cfg *config.Config,
	startTime time.Time,
	skipAttestation bool,
) error {
	fmt.Printf("📥 Downloading artifacts from release b-%s...\n", key[:12])

	// Fetch metadata.json
	meta, err := relClient.FetchMetadata(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to fetch release metadata: %w", err)
	}

	// Fetch SHA256SUMS
	sumsContent, _ := relClient.FetchSHA256Sums(ctx, key)

	// Fetch Attestation Bundle
	bundleBytes, _ := relClient.FetchAttestationBundle(ctx, key)

	verifier := attestation.NewVerifier(attestation.VerificationOptions{
		ExpectedRepo:    cfg.BuilderRepo,
		ExpectedVersion: req.Version,
		ExpectedCommit:  req.Commit,
		SkipAttestation: skipAttestation,
	})

	// Download binaries into cache and verify
	for _, b := range meta.Binaries {
		fmt.Printf("   ⬇️  Downloading %s...\n", b.Name)
		downloadedPath, err := relClient.DownloadBinary(ctx, key, b.Name, snap.GOOS, snap.GOARCH, cacheStore)
		if err != nil {
			return fmt.Errorf("failed to download binary %s: %w", b.Name, err)
		}

		fmt.Printf("   🔒 Verifying %s (SHA-256 + Attestation)...\n", b.Name)
		if err := verifier.VerifyBinary(downloadedPath, b.Name, meta, sumsContent, bundleBytes); err != nil {
			_ = os.Remove(downloadedPath)
			return fmt.Errorf("SECURITY ALERT: Verification failed for %s: %w", b.Name, err)
		}
		fmt.Printf("   ✅ %s verified successfully!\n", b.Name)
	}

	// Save metadata.json to cache
	_ = cacheStore.SaveMetadata(key, meta)

	// Atomic install into GOBIN
	fmt.Printf("⚙️  Installing binaries to %s...\n", snap.GOBIN)
	res, err := inst.InstallBinaries(cacheStore.KeyDir(key), meta, snap.GOBIN, snap.GOOS)
	if err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	printInstallSuccess(res, time.Since(startTime))
	return nil
}

func printInstallSuccess(res *installer.InstallResult, duration time.Duration) {
	fmt.Printf("\n✨ Successfully installed in %.2fs!\n", duration.Seconds())
	for _, b := range res.InstalledBinaries {
		fmt.Printf("   ▶ %s -> %s\n", b.Name, b.Path)
	}

	if res.NotInPATHWarning {
		fmt.Printf("\n⚠️  [PATH Warning] %s is not in your $PATH!\n", res.GOBIN)
		fmt.Printf("   Add it with: export PATH=\"%s:$PATH\"\n", res.GOBIN)
	}
}
