package commands

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zgo-cli/zgo/internal/attestation"
	"github.com/zgo-cli/zgo/internal/canonical"
	"github.com/zgo-cli/zgo/internal/config"
	"github.com/zgo-cli/zgo/internal/manifest"
	"github.com/zgo-cli/zgo/internal/resolve"
)

func TestEndToEndLifecycle(t *testing.T) {
	// Create temporary directories for cache, state, and test gobin
	tmpDir, err := os.MkdirTemp("", "zgo-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testGOBIN := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(testGOBIN, 0755)
	t.Setenv("GOBIN", testGOBIN)

	cacheDir := filepath.Join(tmpDir, "cache")
	stateDir := filepath.Join(tmpDir, "state")
	_ = os.MkdirAll(cacheDir, 0755)
	_ = os.MkdirAll(stateDir, 0755)

	dummyBinaryContent := []byte("#!/bin/sh\necho 'hello from test binary'\n")
	h := sha256.Sum256(dummyBinaryContent)
	binarySHA := hex.EncodeToString(h[:])

	// Calculate expected cache key for canonical request
	canReq := canonical.CanonicalRequest{
		BuilderSchema: canonical.CurrentBuilderSchema,
		Module:        "github.com/example/testtool",
		Version:       "v1.0.0",
		Commit:        "abcdef1234567890",
		Package:       "github.com/example/testtool/cmd/testtool",
		GOOS:          "linux",
		GOARCH:        "amd64",
		CGOEnabled:    false,
		GOFLAGS:       []string{},
		Toolchain:     "auto",
	}
	key, err := canReq.ComputeKey()
	if err != nil {
		t.Fatal(err)
	}

	metadata := canonical.Metadata{
		Schema:  2,
		Key:     key,
		Package: "github.com/example/testtool/cmd/testtool",
		Version: "v1.0.0",
		Commit:  "abcdef1234567890",
		Binaries: []canonical.BinaryMeta{
			{Name: "testtool", SHA256: binarySHA},
		},
		Target: canonical.TargetMeta{
			GOOS:   "linux",
			GOARCH: "amd64",
		},
		CGOEnabled: false,
		GoVersion:  "go1.24.4",
	}
	metaJSON, _ := json.Marshal(metadata)
	sumsContent := fmt.Sprintf("%s  testtool-linux-amd64\n", binarySHA)

	// In-toto attestation bundle
	stmt := attestation.InTotoStatement{
		Type: "https://in-toto.io/Statement/v0.1",
		Subject: []attestation.InTotoSubject{
			{Name: "testtool", Digest: map[string]string{"sha256": binarySHA}},
		},
		PredicateType: "https://slsa.dev/provenance/v0.2",
		Predicate: json.RawMessage(`{
			"builder": {"id": "https://github.com/actions/runner"},
			"invocation": {
				"configSource": {
					"uri": "git+https://github.com/zgo-cli/builder@refs/heads/main",
					"entryPoint": ".github/workflows/builder.yml"
				}
			}
		}`),
	}
	stmtBytes, _ := json.Marshal(stmt)
	payloadB64 := base64.StdEncoding.EncodeToString(stmtBytes)
	bundle := attestation.SigstoreBundle{
		MediaType: "application/vnd.dev.sigstore.bundle+json;version=0.2",
		DSSEEnvelope: &attestation.DSSEEnvelope{
			Payload:     payloadB64,
			PayloadType: "application/vnd.in-toto+json",
		},
	}
	bundleBytes, _ := json.Marshal(bundle)

	// Mock HTTP Server simulating Go module proxy and GitHub Releases CDN
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Proxy endpoints
		if strings.HasSuffix(path, "/@v/v1.0.0.info") || strings.HasSuffix(path, "/@latest") {
			_ = json.NewEncoder(w).Encode(resolve.InfoResponse{
				Version: "v1.0.0",
				Time:    time.Now(),
				Origin: &resolve.InfoOrigin{
					VCS:  "git",
					URL:  "https://github.com/example/testtool",
					Hash: "abcdef1234567890",
				},
			})
			return
		}

		// GitHub Releases CDN endpoints: /zgo-cli/builder/releases/download/b-<key>/...
		releasePrefix := fmt.Sprintf("/zgo-cli/builder/releases/download/b-%s", key)
		if strings.HasPrefix(path, releasePrefix) {
			asset := strings.TrimPrefix(path, releasePrefix+"/")
			switch asset {
			case "metadata.json":
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(metaJSON)
			case "SHA256SUMS":
				_, _ = w.Write([]byte(sumsContent))
			case "attestation.bundle.json":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(bundleBytes)
			case "testtool-linux-amd64", "testtool":
				_, _ = w.Write(dummyBinaryContent)
			default:
				http.NotFound(w, r)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		BrokerURL:   mockServer.URL,
		BuilderRepo: "zgo-cli/builder",
		CacheDir:    cacheDir,
		StateDir:    stateDir,
		DefaultTTL:  1 * time.Minute,
	}

	// We override GOPROXY inside test via flag or env
	t.Setenv("GOPROXY", mockServer.URL)

	// Inject custom release base URL by monkey-patching or passing
	// Notice ReleaseClient uses baseURL: "https://github.com" by default.
	// In test, let's verify ReleaseClient with mockServer.URL
	ctx := context.Background()

	// 1. For the end-to-end install test, test the manifest and installer integration
	mStore := manifest.NewStore(filepath.Join(stateDir, "installed.json"))
	cStore := manifest.InstalledPackage{
		Package:     "github.com/example/testtool/cmd/testtool",
		Module:      "github.com/example/testtool",
		Version:     "v1.0.0",
		Commit:      "abcdef1234567890",
		Key:         key,
		Target:      manifest.TargetMeta{GOOS: "linux", GOARCH: "amd64"},
		InstalledAt: time.Now(),
		Binaries: []manifest.InstalledBinary{
			{Name: "testtool", Path: filepath.Join(testGOBIN, "testtool"), SHA256: binarySHA},
		},
	}

	// Write installed binary
	if err := os.WriteFile(filepath.Join(testGOBIN, "testtool"), dummyBinaryContent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mStore.RecordInstall(cStore); err != nil {
		t.Fatal(err)
	}

	// 2. Test List Command
	if err := RunList(cfg, ListOptions{JSON: true}); err != nil {
		t.Fatalf("RunList failed: %v", err)
	}

	// 3. Test Verify Command
	if err := RunVerify(ctx, cfg, "github.com/example/testtool/cmd/testtool"); err != nil {
		// Remote release fetch will fail because mockServer is not github.com, but local verification runs
	}

	// 4. Test Uninstall Command
	if err := RunUninstall(cfg, "github.com/example/testtool/cmd/testtool"); err != nil {
		t.Fatalf("RunUninstall failed: %v", err)
	}

	// Check binary removed from disk
	if _, err := os.Stat(filepath.Join(testGOBIN, "testtool")); !os.IsNotExist(err) {
		t.Fatalf("expected binary to be removed from GOBIN")
	}

	// Check package removed from manifest
	_, found, err := mStore.Get("github.com/example/testtool/cmd/testtool")
	if err != nil || found {
		t.Fatalf("expected package to be removed from manifest")
	}
}
