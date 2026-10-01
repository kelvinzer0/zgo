package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGOBINResolution(t *testing.T) {
	resolver := &EnvResolver{goEnvMap: make(map[string]string)}

	// 1. Explicit GOBIN
	resolver.goEnvMap["GOBIN"] = "/custom/gobin"
	if got := resolver.resolveGOBIN(""); got != "/custom/gobin" {
		t.Fatalf("expected /custom/gobin, got %s", got)
	}

	// 2. Fallback to first GOPATH
	delete(resolver.goEnvMap, "GOBIN")
	gopath := "/my/gopath1" + string(filepath.ListSeparator) + "/my/gopath2"
	if got := resolver.resolveGOBIN(gopath); got != filepath.Join("/my/gopath1", "bin") {
		t.Fatalf("expected %s, got %s", filepath.Join("/my/gopath1", "bin"), got)
	}

	// 3. Fallback to HOME/go/bin
	home, _ := os.UserHomeDir()
	expectedHomeBin := filepath.Join(home, "go", "bin")
	if got := resolver.resolveGOBIN(""); got != expectedHomeBin {
		t.Fatalf("expected %s, got %s", expectedHomeBin, got)
	}
}

func TestResolveSnapshotWarningsInPublicMode(t *testing.T) {
	resolver := &EnvResolver{goEnvMap: make(map[string]string)}
	resolver.goEnvMap["GOPROXY"] = "https://custom-proxy.internal"
	resolver.goEnvMap["GOPRIVATE"] = "gitlab.internal/*"

	snap, err := resolver.ResolveSnapshot(FlagsOption{SelfHosted: false})
	if err != nil {
		t.Fatalf("ResolveSnapshot failed: %v", err)
	}

	if len(snap.Warnings) < 2 {
		t.Fatalf("expected at least 2 warnings for custom GOPROXY and GOPRIVATE in public mode, got %d: %v", len(snap.Warnings), snap.Warnings)
	}

	// In public mode GOPROXY must default to proxy.golang.org
	if snap.GOPROXY != "https://proxy.golang.org,direct" {
		t.Fatalf("expected proxy.golang.org, got %s", snap.GOPROXY)
	}
}

func TestResolveSnapshotSelfHosted(t *testing.T) {
	resolver := &EnvResolver{goEnvMap: make(map[string]string)}
	resolver.goEnvMap["GOPROXY"] = "https://custom-proxy.internal"
	resolver.goEnvMap["GOPRIVATE"] = "gitlab.internal/*"

	snap, err := resolver.ResolveSnapshot(FlagsOption{SelfHosted: true})
	if err != nil {
		t.Fatalf("ResolveSnapshot failed: %v", err)
	}

	if len(snap.Warnings) > 0 {
		t.Fatalf("expected no warnings in self-hosted mode, got %v", snap.Warnings)
	}
	if snap.GOPROXY != "https://custom-proxy.internal" {
		t.Fatalf("expected custom proxy preserved in self-hosted mode, got %s", snap.GOPROXY)
	}
}
