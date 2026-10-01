package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/zgo-cli/zgo/internal/cache"
	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/env"
	"github.com/zgo-cli/zgo/internal/manifest"
)

func RunDoctor(ctx context.Context, cfg *config.Config) error {
	fmt.Println("🩺 Running ZGo Diagnostics...")
	fmt.Println("-------------------------------------------")

	hasErrors := false

	// 1. Architecture & Platform
	fmt.Printf("💻 Platform: %s/%s (runtime: %s)\n", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// 2. GOBIN & PATH Check
	envResolver := env.NewEnvResolver()
	snap, err := envResolver.ResolveSnapshot(env.FlagsOption{})
	if err != nil {
		fmt.Printf("❌ Failed to resolve environment: %v\n", err)
		hasErrors = true
	} else {
		fmt.Printf("📁 GOBIN: %s\n", snap.GOBIN)
		if env.CheckGOBINInPATH(snap.GOBIN) {
			fmt.Println("   ✅ GOBIN is present in your PATH.")
		} else {
			fmt.Printf("   ⚠️  GOBIN is NOT in your PATH. Add it via: export PATH=\"%s:$PATH\"\n", snap.GOBIN)
		}
	}

	// 3. Cache directory check
	fmt.Printf("💾 Cache Directory: %s\n", cfg.CacheDir)
	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		fmt.Printf("   ❌ Cache directory is not writable: %v\n", err)
		hasErrors = true
	} else {
		cStore := cache.NewStore(cfg.CacheDir)
		size, _ := cStore.TotalSize()
		fmt.Printf("   ✅ Cache directory accessible (current size: %.2f MB)\n", float64(size)/(1024*1024))
	}

	// 4. State / Manifest directory check
	fmt.Printf("📋 State Directory: %s\n", cfg.StateDir)
	manifestPath := filepath.Join(cfg.StateDir, "installed.json")
	if err := os.MkdirAll(cfg.StateDir, 0755); err != nil {
		fmt.Printf("   ❌ State directory is not writable: %v\n", err)
		hasErrors = true
	} else {
		mStore := manifest.NewStore(manifestPath)
		m, err := mStore.Load()
		if err != nil {
			fmt.Printf("   ❌ Manifest file error: %v\n", err)
			hasErrors = true
		} else {
			fmt.Printf("   ✅ Manifest store accessible (%d packages installed)\n", len(m.Packages))
		}
	}

	// 5. Network connectivity
	httpClient := &http.Client{Timeout: 5 * time.Second}

	// Go proxy
	checkEndpoint(httpClient, "https://proxy.golang.org", "Go Module Proxy (proxy.golang.org)", &hasErrors)

	// GitHub Releases CDN
	checkEndpoint(httpClient, "https://github.com", "GitHub Releases CDN (github.com)", &hasErrors)

	// Broker
	checkEndpoint(httpClient, cfg.BrokerURL+"/health", fmt.Sprintf("Build Broker (%s)", cfg.BrokerURL), nil)

	fmt.Println("-------------------------------------------")
	if hasErrors {
		fmt.Println("⚠️  Some diagnostic checks failed. Please review issues above.")
		return fmt.Errorf("doctor checks failed")
	}
	fmt.Println("✨ All essential diagnostic checks passed! ZGo is ready for remote installations.")
	return nil
}

func checkEndpoint(client *http.Client, url string, name string, hasErrors *bool) {
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("🌐 %s: ⚠️ Unreachable (%v)\n", name, err)
		if hasErrors != nil {
			*hasErrors = true
		}
		return
	}
	resp.Body.Close()
	fmt.Printf("🌐 %s: ✅ Reachable (HTTP %d)\n", name, resp.StatusCode)
}
