package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zgo-cli/zgo/internal/canonical"
)

type SelfHostedClient struct {
	HTTPClient *http.Client
	Repo       string
	Token      string
}

func NewSelfHostedClient(repo string, token string) *SelfHostedClient {
	return &SelfHostedClient{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Repo:       repo,
		Token:      token,
	}
}

// DispatchBuild directly triggers workflow_dispatch on the user's self-hosted GitHub repository
func (s *SelfHostedClient) DispatchBuild(ctx context.Context, req *canonical.CanonicalRequest, key string) error {
	if s.Token == "" {
		return fmt.Errorf("self-hosted mode requires GITHUB_TOKEN or ZGO_GITHUB_TOKEN")
	}
	if s.Repo == "" {
		return fmt.Errorf("self-hosted mode requires ZGO_BUILDER_REPO (e.g. username/zgo-builder)")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/actions/workflows/builder.yml/dispatches", s.Repo)

	payload := map[string]interface{}{
		"ref": "main",
		"inputs": map[string]string{
			"key":         key,
			"package":     req.Package,
			"version":     req.Version,
			"goos":        req.GOOS,
			"goarch":      req.GOARCH,
			"cgo_enabled": fmt.Sprintf("%v", req.CGOEnabled),
			"goflags":     strings.Join(req.GOFLAGS, " "),
			"toolchain":   req.Toolchain,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.Token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := s.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to dispatch self-hosted workflow: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API rejected workflow dispatch (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
