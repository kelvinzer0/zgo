package resolve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		input   string
		wantPkg string
		wantVer string
	}{
		{"github.com/charmbracelet/glow", "github.com/charmbracelet/glow", "latest"},
		{"github.com/charmbracelet/glow@v1.5.0", "github.com/charmbracelet/glow", "v1.5.0"},
		{"golang.org/x/tools/cmd/goimports@latest", "golang.org/x/tools/cmd/goimports", "latest"},
	}

	for _, tt := range tests {
		pkg, ver := ParseTarget(tt.input)
		if pkg != tt.wantPkg || ver != tt.wantVer {
			t.Errorf("ParseTarget(%q) = (%q, %q), want (%q, %q)", tt.input, pkg, ver, tt.wantPkg, tt.wantVer)
		}
	}
}

func TestEscapeModulePath(t *testing.T) {
	if got := EscapeModulePath("github.com/Azure/azure-sdk-for-go"); got != "github.com/!azure/azure-sdk-for-go" {
		t.Errorf("EscapeModulePath failed: got %s", got)
	}
}

func TestResolveMockProxy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github.com/test/tool/@latest":
			json.NewEncoder(w).Encode(InfoResponse{
				Version: "v1.2.3",
				Time:    time.Now(),
				Origin: &InfoOrigin{
					VCS:  "git",
					URL:  "https://github.com/test/tool",
					Hash: "fedcba9876543210",
				},
			})
		case "/github.com/test/tool/@v/v1.2.3.info":
			json.NewEncoder(w).Encode(InfoResponse{
				Version: "v1.2.3",
				Time:    time.Now(),
				Origin: &InfoOrigin{
					Hash: "fedcba9876543210",
				},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	tmpDir, err := os.MkdirTemp("", "zgo-test-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	resolver := NewResolver(ts.URL, tmpDir)

	res, err := resolver.Resolve(context.Background(), "github.com/test/tool", "latest")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if res.Version != "v1.2.3" {
		t.Errorf("expected version v1.2.3, got %s", res.Version)
	}
	if res.Commit != "fedcba9876543210" {
		t.Errorf("expected commit fedcba9876543210, got %s", res.Commit)
	}

	// Verify cached entry works
	cached, ok := resolver.getCachedLatest("github.com/test/tool")
	if !ok || cached.Version != "v1.2.3" {
		t.Fatalf("expected cached latest to be found and match")
	}
}
