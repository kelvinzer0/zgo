package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zgo-cli/zgo/internal/cache"
	"github.com/zgo-cli/zgo/internal/canonical"
)

// ReleaseClient interacts directly with GitHub Releases for fast, CDN-backed, unauthenticated downloads
type ReleaseClient struct {
	HTTPClient *http.Client
	Repo       string // e.g. "zgo-cli/builder" or user self-hosted repo
	BaseURL    string // default: "https://github.com"
}

func NewReleaseClient(repo string, baseURL string) *ReleaseClient {
	if repo == "" {
		repo = "kelvinzer0/zgo"
	}
	if baseURL == "" {
		baseURL = "https://github.com"
	}
	return &ReleaseClient{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Repo:       repo,
		BaseURL:    strings.TrimSuffix(baseURL, "/"),
	}
}

// ReleaseTag returns the release tag for a cache key
func (c *ReleaseClient) ReleaseTag(key string) string {
	return "b-" + key
}

// ReleaseAssetURL returns the direct download URL for an asset
func (c *ReleaseClient) ReleaseAssetURL(key string, filename string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s",
		c.BaseURL, c.Repo, c.ReleaseTag(key), filename)
}

// CheckReleaseExists checks via HTTP HEAD if release metadata exists (Cache HIT check)
func (c *ReleaseClient) CheckReleaseExists(ctx context.Context, key string) (bool, error) {
	metaURL := c.ReleaseAssetURL(key, "metadata.json")
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, metaURL, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	// GitHub may redirect releases/download, so if HEAD returned 302, follow it
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		return true, nil
	}

	return false, nil
}

// FetchMetadata downloads and parses metadata.json from GitHub Releases
func (c *ReleaseClient) FetchMetadata(ctx context.Context, key string) (*canonical.Metadata, error) {
	metaURL := c.ReleaseAssetURL(key, "metadata.json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata from release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata.json returned HTTP %d", resp.StatusCode)
	}

	var meta canonical.Metadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("failed to decode metadata.json: %w", err)
	}

	return &meta, nil
}

// FetchSHA256Sums downloads SHA256SUMS file from GitHub Releases
func (c *ReleaseClient) FetchSHA256Sums(ctx context.Context, key string) (string, error) {
	sumsURL := c.ReleaseAssetURL(key, "SHA256SUMS")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHA256SUMS returned HTTP %d", resp.StatusCode)
	}

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// FetchAttestationBundle downloads attestation.bundle.json if available
func (c *ReleaseClient) FetchAttestationBundle(ctx context.Context, key string) ([]byte, error) {
	bundleURL := c.ReleaseAssetURL(key, "attestation.bundle.json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Could be named attestation.sigstore.json
		altURL := c.ReleaseAssetURL(key, "attestation.sigstore.json")
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, altURL, nil)
		resp2, err2 := c.HTTPClient.Do(req2)
		if err2 == nil && resp2.StatusCode == http.StatusOK {
			defer resp2.Body.Close()
			return io.ReadAll(resp2.Body)
		}
		if err2 == nil {
			resp2.Body.Close()
		}
		return nil, fmt.Errorf("attestation bundle not found (status %d)", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// DownloadBinary downloads a binary asset and saves it into the local cache store
func (c *ReleaseClient) DownloadBinary(
	ctx context.Context,
	key string,
	binaryName string,
	targetGOOS string,
	targetGOARCH string,
	cacheStore *cache.Store,
) (string, error) {
	isWindows := (strings.ToLower(targetGOOS) == "windows")
	assetCandidates := []string{
		fmt.Sprintf("%s-%s-%s", binaryName, targetGOOS, targetGOARCH),
		binaryName,
	}
	if isWindows {
		assetCandidates = []string{
			fmt.Sprintf("%s-%s-%s.exe", binaryName, targetGOOS, targetGOARCH),
			fmt.Sprintf("%s.exe", binaryName),
			binaryName,
		}
	}

	var lastErr error
	for _, candidate := range assetCandidates {
		assetURL := c.ReleaseAssetURL(key, candidate)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("asset %s returned HTTP %d", candidate, resp.StatusCode)
			continue
		}

		// Save to cache under canonical binary name (and .exe if windows)
		destName := binaryName
		if isWindows && !strings.HasSuffix(destName, ".exe") {
			destName += ".exe"
		}

		savedPath, err := cacheStore.SaveArtifact(key, destName, resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to save downloaded binary to cache: %w", err)
		}

		return savedPath, nil
	}

	return "", fmt.Errorf("failed to download binary %s: %w", binaryName, lastErr)
}
