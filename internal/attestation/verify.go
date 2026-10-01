package attestation

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zgo-cli/zgo/internal/canonical"
)

// InTotoSubject describes a subject in an In-Toto statement
type InTotoSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// InTotoStatement represents an in-toto attestation statement
type InTotoStatement struct {
	Type          string          `json:"_type"`
	Subject       []InTotoSubject `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

// DSSEEnvelope represents a Dead Simple Signing Envelope
type DSSEEnvelope struct {
	Payload     string `json:"payload"`
	PayloadType string `json:"payloadType"`
}

// SigstoreBundle represents a Sigstore / GitHub attestation bundle
type SigstoreBundle struct {
	MediaType    string        `json:"mediaType"`
	DSSEEnvelope *DSSEEnvelope `json:"dsseEnvelope,omitempty"`
}

// VerificationOptions provides configuration for the verification process
type VerificationOptions struct {
	ExpectedRepo    string // e.g. "zgo-cli/builder" or self-hosted repo
	ExpectedCommit  string
	ExpectedVersion string
	SkipAttestation bool   // Only for offline testing/dry-runs
}

// Verifier handles integrity and provenance verification of built artifacts
type Verifier struct {
	options VerificationOptions
}

func NewVerifier(opts VerificationOptions) *Verifier {
	if opts.ExpectedRepo == "" {
		opts.ExpectedRepo = "zgo-cli/builder"
	}
	return &Verifier{options: opts}
}

// ComputeFileSHA256 returns the hex-encoded SHA-256 of the given file path
func ComputeFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyBinary performs all verification checks according to ZGo v2 spec:
// 1. SHA256 integrity check against metadata
// 2. SHA256 integrity check against SHA256SUMS
// 3. Version match check
// 4. Commit match check (if resolved commit is present)
// 5. Build provenance attestation verification (DSSE / Sigstore)
func (v *Verifier) VerifyBinary(
	binaryPath string,
	binaryName string,
	meta *canonical.Metadata,
	sha256SumsContent string,
	attestationBundleBytes []byte,
) error {
	// 1. Compute binary SHA256
	actualSHA256, err := ComputeFileSHA256(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to compute binary sha256: %w", err)
	}

	// 2. Check metadata match
	var matchedMeta *canonical.BinaryMeta
	for _, b := range meta.Binaries {
		if b.Name == binaryName || b.Name+".exe" == binaryName {
			matchedMeta = &b
			break
		}
	}
	if matchedMeta == nil {
		return fmt.Errorf("verification failed: binary %q not listed in metadata.json", binaryName)
	}

	if !strings.EqualFold(actualSHA256, matchedMeta.SHA256) {
		return fmt.Errorf("integrity violation: SHA256 mismatch for %s: got %s, metadata specifies %s",
			binaryName, actualSHA256, matchedMeta.SHA256)
	}

	// 3. Check SHA256SUMS file if provided
	if sha256SumsContent != "" {
		foundInSums := false
		lines := strings.Split(sha256SumsContent, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				sum := parts[0]
				file := strings.TrimPrefix(parts[1], "*")
				if file == binaryName || strings.HasPrefix(file, binaryName+"-") {
					foundInSums = true
					if !strings.EqualFold(actualSHA256, sum) {
						return fmt.Errorf("integrity violation: SHA256SUMS mismatch for %s: expected %s, got %s",
							binaryName, sum, actualSHA256)
					}
					break
				}
			}
		}
		if !foundInSums && len(lines) > 0 && strings.TrimSpace(sha256SumsContent) != "" {
			// Check if file is mentioned anywhere
			for _, line := range lines {
				if strings.Contains(line, actualSHA256) {
					foundInSums = true
					break
				}
			}
		}
	}

	// 4. Verify Version
	if v.options.ExpectedVersion != "" && v.options.ExpectedVersion != "latest" {
		if meta.Version != v.options.ExpectedVersion {
			return fmt.Errorf("version mismatch: expected %s, release metadata has %s",
				v.options.ExpectedVersion, meta.Version)
		}
	}

	// 5. Verify Commit (if available)
	if v.options.ExpectedCommit != "" && meta.Commit != "" {
		expectedPrefix := v.options.ExpectedCommit
		if len(expectedPrefix) > 7 {
			expectedPrefix = expectedPrefix[:7]
		}
		metaCommitPrefix := meta.Commit
		if len(metaCommitPrefix) > 7 {
			metaCommitPrefix = metaCommitPrefix[:7]
		}

		if !strings.HasPrefix(meta.Commit, expectedPrefix) && !strings.HasPrefix(v.options.ExpectedCommit, metaCommitPrefix) {
			return fmt.Errorf("commit mismatch: resolved %s, built commit %s",
				v.options.ExpectedCommit, meta.Commit)
		}
	}

	// 6. Verify Attestation Provenance
	if !v.options.SkipAttestation && len(attestationBundleBytes) > 0 {
		if err := v.verifyAttestationBundle(attestationBundleBytes, actualSHA256, binaryName); err != nil {
			return fmt.Errorf("build attestation verification failed: %w", err)
		}
	}

	return nil
}

// verifyAttestationBundle parses the Sigstore bundle and verifies in-toto claims
func (v *Verifier) verifyAttestationBundle(bundleBytes []byte, expectedSHA256 string, binaryName string) error {
	var bundle SigstoreBundle
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		// Could be a raw InToto statement
		var stmt InTotoStatement
		if err2 := json.Unmarshal(bundleBytes, &stmt); err2 == nil && len(stmt.Subject) > 0 {
			return v.verifyInTotoStatement(&stmt, expectedSHA256, binaryName)
		}
		return fmt.Errorf("failed to parse attestation bundle: %w", err)
	}

	if bundle.DSSEEnvelope == nil || bundle.DSSEEnvelope.Payload == "" {
		return fmt.Errorf("attestation bundle missing DSSE envelope payload")
	}

	payloadBytes, err := base64.StdEncoding.DecodeString(bundle.DSSEEnvelope.Payload)
	if err != nil {
		return fmt.Errorf("failed to decode DSSE payload: %w", err)
	}

	var stmt InTotoStatement
	if err := json.Unmarshal(payloadBytes, &stmt); err != nil {
		return fmt.Errorf("failed to decode in-toto statement from envelope: %w", err)
	}

	return v.verifyInTotoStatement(&stmt, expectedSHA256, binaryName)
}

func (v *Verifier) verifyInTotoStatement(stmt *InTotoStatement, expectedSHA256 string, binaryName string) error {
	// Verify that the subject hash matches our binary
	subjectMatched := false
	for _, sub := range stmt.Subject {
		if hash, ok := sub.Digest["sha256"]; ok {
			if strings.EqualFold(hash, expectedSHA256) {
				subjectMatched = true
				break
			}
		}
	}
	if !subjectMatched {
		return fmt.Errorf("attestation subject digest does not match binary sha256 (%s)", expectedSHA256)
	}

	// Verify predicate contains official builder workflow & repository
	predicateStr := string(stmt.Predicate)
	if v.options.ExpectedRepo != "" {
		expectedRepoLower := strings.ToLower(v.options.ExpectedRepo)
		if !strings.Contains(strings.ToLower(predicateStr), expectedRepoLower) {
			return fmt.Errorf("attestation builder repository mismatch: expected builder repo %q in predicate",
				v.options.ExpectedRepo)
		}
	}

	// Check workflow filename in predicate
	if !strings.Contains(predicateStr, "builder.yml") && !strings.Contains(predicateStr, ".github/workflows") {
		return fmt.Errorf("attestation does not originate from standard builder workflow (.github/workflows/builder.yml)")
	}

	return nil
}
