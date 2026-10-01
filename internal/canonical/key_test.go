package canonical

import (
	"testing"
)

func TestValidateAndNormalizeGOFLAGS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", false},
		{"trimpath", "-trimpath", false},
		{"buildvcs", "-buildvcs=auto", false},
		{"valid tags", "-tags=integration,netgo", false},
		{"valid ldflags", `-ldflags="-s -w"`, false},
		{"valid ldflags with -X", `-ldflags="-s -w -X main.version=1.0.0"`, false},
		{"gcflags safe", "-gcflags=-N", false},
		{"forbidden toolexec", "-toolexec=evil", true},
		{"forbidden exec", "-exec=evil", true},
		{"forbidden modfile", "-modfile=/tmp/go.mod", true},
		{"forbidden local path", "/etc/passwd", true},
		{"unsupported flag", "-custom-unsupported-flag", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateAndNormalizeGOFLAGS(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAndNormalizeGOFLAGS(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && len(got) > 0 && tt.input != "" {
				// verify it parsed successfully
			}
		})
	}
}

func TestComputeKeyDeterministic(t *testing.T) {
	req1 := CanonicalRequest{
		Module:     "github.com/charmbracelet/glow",
		Version:    "v1.5.0",
		Package:    "github.com/charmbracelet/glow",
		GOOS:       "linux",
		GOARCH:     "amd64",
		CGOEnabled: false,
		GOFLAGS:    []string{"-tags=netgo", "-trimpath"},
	}

	req2 := CanonicalRequest{
		Module:     "github.com/charmbracelet/glow",
		Version:    "v1.5.0",
		Package:    "github.com/charmbracelet/glow",
		GOOS:       "linux",
		GOARCH:     "amd64",
		CGOEnabled: false,
		GOFLAGS:    []string{"-trimpath", "-tags=netgo"}, // reversed order
	}

	key1, err1 := req1.ComputeKey()
	if err1 != nil {
		t.Fatalf("req1.ComputeKey() err = %v", err1)
	}

	key2, err2 := req2.ComputeKey()
	if err2 != nil {
		t.Fatalf("req2.ComputeKey() err = %v", err2)
	}

	if key1 != key2 {
		t.Fatalf("expected identical keys for reordered flags: %s != %s", key1, key2)
	}
	if len(key1) != 64 {
		t.Fatalf("expected sha256 hex length 64, got %d", len(key1))
	}
}
