package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const CurrentBuilderSchema = 2

// CanonicalRequest captures all build-affecting parameters in a deterministic structure.
type CanonicalRequest struct {
	BuilderSchema int      `json:"builder_schema"`
	Module        string   `json:"module"`
	Version       string   `json:"version"` // concrete version (e.g. v1.2.3, not latest)
	Commit        string   `json:"commit,omitempty"`
	Package       string   `json:"package"` // full package import path, e.g. github.com/user/tool/cmd/tool
	GOOS          string   `json:"goos"`
	GOARCH        string   `json:"goarch"`
	GOARM         string   `json:"goarm,omitempty"`
	GOAMD64       string   `json:"goamd64,omitempty"`
	GO386         string   `json:"go386,omitempty"`
	GOMIPS        string   `json:"gomips,omitempty"`
	GOMIPS64      string   `json:"gomips64,omitempty"`
	GOPPC64       string   `json:"goppc64,omitempty"`
	CGOEnabled    bool     `json:"cgo_enabled"`
	GOFLAGS       []string `json:"goflags"`
	Toolchain     string   `json:"toolchain,omitempty"`
}

// ComputeKey returns the SHA-256 hash of the canonical request representation.
func (r *CanonicalRequest) ComputeKey() (string, error) {
	// Normalize fields for deterministic hashing
	normalized := *r
	normalized.BuilderSchema = CurrentBuilderSchema
	normalized.Module = strings.TrimSpace(normalized.Module)
	normalized.Version = strings.TrimSpace(normalized.Version)
	normalized.Package = strings.TrimSpace(normalized.Package)
	normalized.GOOS = strings.ToLower(strings.TrimSpace(normalized.GOOS))
	normalized.GOARCH = strings.ToLower(strings.TrimSpace(normalized.GOARCH))
	normalized.Toolchain = strings.TrimSpace(normalized.Toolchain)

	// Sort GOFLAGS deterministically
	flagsCopy := make([]string, len(normalized.GOFLAGS))
	copy(flagsCopy, normalized.GOFLAGS)
	sort.Strings(flagsCopy)
	normalized.GOFLAGS = flagsCopy

	// Serialize with standard JSON encoding
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("failed to serialize canonical request: %w", err)
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// BinaryMeta holds metadata for each binary produced
type BinaryMeta struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// TargetMeta holds GOOS and GOARCH
type TargetMeta struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

// Metadata represents the metadata.json emitted by the builder in GitHub Releases
type Metadata struct {
	Schema      int          `json:"schema"`
	Key         string       `json:"key"`
	Package     string       `json:"package"`
	Version     string       `json:"version"`
	Commit      string       `json:"commit"`
	Binaries    []BinaryMeta `json:"binaries"`
	Target      TargetMeta   `json:"target"`
	CGOEnabled  bool         `json:"cgo_enabled"`
	GOFLAGS     []string     `json:"goflags"`
	GoVersion   string       `json:"go_version"`
	WorkflowRun int64        `json:"workflow_run"`
}
