package attestation

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zgo-cli/zgo/internal/canonical"
)

func TestVerifyBinaryIntegrity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "zgo-verify-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dummyBin := filepath.Join(tmpDir, "dummy-tool")
	binContent := []byte("binary executable content for testing zgo verification")
	if err := os.WriteFile(dummyBin, binContent, 0755); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(binContent)
	actualHash := hex.EncodeToString(h[:])

	meta := &canonical.Metadata{
		Schema:  2,
		Package: "github.com/example/tool",
		Version: "v1.0.0",
		Commit:  "1234567890abcdef",
		Binaries: []canonical.BinaryMeta{
			{Name: "dummy-tool", SHA256: actualHash},
		},
	}

	sumsContent := actualHash + "  dummy-tool\n"

	// Construct simulated in-toto statement and Sigstore envelope
	stmt := InTotoStatement{
		Type: "https://in-toto.io/Statement/v0.1",
		Subject: []InTotoSubject{
			{Name: "dummy-tool", Digest: map[string]string{"sha256": actualHash}},
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
	bundle := SigstoreBundle{
		MediaType: "application/vnd.dev.sigstore.bundle+json;version=0.2",
		DSSEEnvelope: &DSSEEnvelope{
			Payload:     payloadB64,
			PayloadType: "application/vnd.in-toto+json",
		},
	}
	bundleBytes, _ := json.Marshal(bundle)

	verifier := NewVerifier(VerificationOptions{
		ExpectedRepo:    "zgo-cli/builder",
		ExpectedVersion: "v1.0.0",
		ExpectedCommit:  "1234567890abcdef",
	})

	// Test Success
	err = verifier.VerifyBinary(dummyBin, "dummy-tool", meta, sumsContent, bundleBytes)
	if err != nil {
		t.Fatalf("expected verification to succeed, got: %v", err)
	}

	// Test SHA mismatch
	corruptedMeta := *meta
	corruptedMeta.Binaries = []canonical.BinaryMeta{{Name: "dummy-tool", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}}
	err = verifier.VerifyBinary(dummyBin, "dummy-tool", &corruptedMeta, sumsContent, bundleBytes)
	if err == nil {
		t.Fatalf("expected error on SHA mismatch, got nil")
	}

	// Test Commit mismatch
	mismatchCommitVerifier := NewVerifier(VerificationOptions{
		ExpectedRepo:    "zgo-cli/builder",
		ExpectedVersion: "v1.0.0",
		ExpectedCommit:  "9999999999999999",
	})
	err = mismatchCommitVerifier.VerifyBinary(dummyBin, "dummy-tool", meta, sumsContent, bundleBytes)
	if err == nil {
		t.Fatalf("expected error on commit mismatch, got nil")
	}
}
