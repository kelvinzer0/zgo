package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// InfoResponse mirrors the proxy.golang.org /@v/<ver>.info JSON response
type InfoResponse struct {
	Version string      `json:"Version"`
	Time    time.Time   `json:"Time"`
	Origin  *InfoOrigin `json:"Origin,omitempty"`
}

type InfoOrigin struct {
	VCS  string `json:"VCS,omitempty"`
	URL  string `json:"URL,omitempty"`
	Hash string `json:"Hash,omitempty"`
	Ref  string `json:"Ref,omitempty"`
}

// ResolvedTarget contains the resolved module, concrete version, commit, and package
type ResolvedTarget struct {
	Module  string
	Version string
	Commit  string
	Package string
}

// TTL for @latest cache
const LatestCacheTTL = 5 * time.Minute

type CacheEntry struct {
	Module    string    `json:"module"`
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Timestamp time.Time `json:"timestamp"`
}

type Resolver struct {
	HTTPClient *http.Client
	ProxyURL   string
	CacheDir   string
	mu         sync.Mutex
}

func NewResolver(proxyURL string, cacheDir string) *Resolver {
	if proxyURL == "" {
		proxyURL = "https://proxy.golang.org"
	}
	// Take first proxy if comma-separated (e.g. https://proxy.golang.org,direct)
	proxies := strings.Split(proxyURL, ",")
	chosenProxy := strings.TrimRight(strings.TrimSpace(proxies[0]), "/")

	if cacheDir == "" {
		userCache, err := os.UserCacheDir()
		if err == nil {
			cacheDir = filepath.Join(userCache, "zgo")
		} else {
			cacheDir = filepath.Join(os.TempDir(), "zgo-cache")
		}
	}

	return &Resolver{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		ProxyURL:   chosenProxy,
		CacheDir:   cacheDir,
	}
}

// ParseTarget parses a pkg[@ver] argument into package path and raw version
func ParseTarget(arg string) (pkgPath string, version string) {
	arg = strings.TrimSpace(arg)
	if idx := strings.LastIndex(arg, "@"); idx != -1 {
		return arg[:idx], arg[idx+1:]
	}
	return arg, "latest"
}

// EscapeModulePath escapes a module path according to Go module proxy specification:
// Any uppercase letter is replaced with '!' followed by the lowercase letter.
func EscapeModulePath(path string) string {
	var sb strings.Builder
	for _, r := range path {
		if unicode.IsUpper(r) {
			sb.WriteRune('!')
			sb.WriteRune(unicode.ToLower(r))
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// Resolve identifies the module for a given package and resolves concrete version and commit
func (r *Resolver) Resolve(ctx context.Context, rawPkg string, rawVer string) (*ResolvedTarget, error) {
	rawPkg = strings.TrimSuffix(strings.TrimSpace(rawPkg), "/")
	if rawPkg == "" {
		return nil, fmt.Errorf("empty package path")
	}
	if rawVer == "" {
		rawVer = "latest"
	}

	// Check local TTL cache if version is @latest
	if rawVer == "latest" {
		if cached, ok := r.getCachedLatest(rawPkg); ok {
			return cached, nil
		}
	}

	// 1. Find module path by probing proxy
	modulePath, err := r.findModulePath(ctx, rawPkg, rawVer)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module for package %s: %w", rawPkg, err)
	}

	// 2. Fetch version info
	escapedMod := EscapeModulePath(modulePath)
	var endpoint string
	if rawVer == "latest" {
		endpoint = fmt.Sprintf("%s/%s/@latest", r.ProxyURL, escapedMod)
	} else {
		escapedVer := url.PathEscape(rawVer)
		endpoint = fmt.Sprintf("%s/%s/@v/%s.info", r.ProxyURL, escapedMod, escapedVer)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxy request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("proxy returned status %d for %s: %s", resp.StatusCode, endpoint, string(body))
	}

	var info InfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode proxy info response: %w", err)
	}

	commit := ""
	if info.Origin != nil && info.Origin.Hash != "" {
		commit = info.Origin.Hash
	} else {
		// Try to extract commit from pseudo-version if applicable (e.g., v0.0.0-20230101010101-123456789abc)
		parts := strings.Split(info.Version, "-")
		if len(parts) >= 3 && len(parts[len(parts)-1]) >= 12 {
			commit = parts[len(parts)-1]
		}
	}

	res := &ResolvedTarget{
		Module:  modulePath,
		Version: info.Version,
		Commit:  commit,
		Package: rawPkg,
	}

	// Save to local cache if latest
	if rawVer == "latest" {
		r.saveCachedLatest(rawPkg, res)
	}

	return res, nil
}

// findModulePath checks candidate prefixes for a package path against the module proxy
func (r *Resolver) findModulePath(ctx context.Context, pkg string, ver string) (string, error) {
	// Candidate prefixes from longest to shortest
	parts := strings.Split(pkg, "/")
	// Minimum module prefix typically has a domain and name, e.g. domain/name (len >= 2)
	for i := len(parts); i >= 2; i-- {
		candidate := strings.Join(parts[:i], "/")
		escaped := EscapeModulePath(candidate)

		var testURL string
		if ver == "latest" {
			testURL = fmt.Sprintf("%s/%s/@latest", r.ProxyURL, escaped)
		} else {
			testURL = fmt.Sprintf("%s/%s/@v/%s.info", r.ProxyURL, escaped, url.PathEscape(ver))
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodHead, testURL, nil)
		if err != nil {
			continue
		}
		resp, err := r.HTTPClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return candidate, nil
			}
		}

		// Also try GET /@v/list as a fallback test
		listURL := fmt.Sprintf("%s/%s/@v/list", r.ProxyURL, escaped)
		reqList, err := http.NewRequestWithContext(ctx, http.MethodHead, listURL, nil)
		if err == nil {
			respList, err := r.HTTPClient.Do(reqList)
			if err == nil {
				respList.Body.Close()
				if respList.StatusCode == http.StatusOK {
					return candidate, nil
				}
			}
		}
	}

	// Default fallback: if pkg has 3 or fewer elements, assume it is the module itself
	if len(parts) <= 3 {
		return pkg, nil
	}
	// Fallback to domain/user/repo
	return strings.Join(parts[:3], "/"), nil
}

func (r *Resolver) getCachedLatest(pkg string) (*ResolvedTarget, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cacheFile := filepath.Join(r.CacheDir, "resolve_latest.json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, false
	}

	var cacheMap map[string]CacheEntry
	if err := json.Unmarshal(data, &cacheMap); err != nil {
		return nil, false
	}

	entry, found := cacheMap[pkg]
	if !found {
		return nil, false
	}

	if time.Since(entry.Timestamp) < LatestCacheTTL {
		return &ResolvedTarget{
			Module:  entry.Module,
			Version: entry.Version,
			Commit:  entry.Commit,
			Package: pkg,
		}, true
	}

	return nil, false
}

func (r *Resolver) saveCachedLatest(pkg string, res *ResolvedTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = os.MkdirAll(r.CacheDir, 0755)
	cacheFile := filepath.Join(r.CacheDir, "resolve_latest.json")

	cacheMap := make(map[string]CacheEntry)
	data, err := os.ReadFile(cacheFile)
	if err == nil {
		_ = json.Unmarshal(data, &cacheMap)
	}

	cacheMap[pkg] = CacheEntry{
		Module:    res.Module,
		Version:   res.Version,
		Commit:    res.Commit,
		Timestamp: time.Now(),
	}

	encoded, err := json.MarshalIndent(cacheMap, "", "  ")
	if err == nil {
		_ = os.WriteFile(cacheFile, encoded, 0644)
	}
}
