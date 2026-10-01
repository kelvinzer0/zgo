package client

import (
	"bufio"
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

// BuildStatus represents status response from broker
type BuildStatus struct {
	Key         string    `json:"key"`
	Status      string    `json:"status"` // queued, building, publishing, completed, failed
	Message     string    `json:"message,omitempty"`
	WorkflowRun int64     `json:"workflow_run,omitempty"`
	RunURL      string    `json:"run_url,omitempty"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type BrokerClient struct {
	HTTPClient *http.Client
	BrokerURL  string
}

func NewBrokerClient(brokerURL string) *BrokerClient {
	if brokerURL == "" {
		brokerURL = "https://broker.zgo.dev"
	}
	return &BrokerClient{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BrokerURL:  strings.TrimSuffix(brokerURL, "/"),
	}
}

// BuildResponse represents the response from POST /build
type BuildResponse struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	RunURL  string `json:"run_url,omitempty"`
}

// RequestBuild sends the canonical build request to the broker
func (b *BrokerClient) RequestBuild(ctx context.Context, req *canonical.CanonicalRequest) (*BuildResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode canonical request: %w", err)
	}

	buildURL := fmt.Sprintf("%s/build", b.BrokerURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, buildURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "zgo-cli/2.0")

	resp, err := b.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to reach build broker at %s: %w", b.BrokerURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("broker rejected build (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var buildResp BuildResponse
	if err := json.Unmarshal(bodyBytes, &buildResp); err != nil {
		return nil, fmt.Errorf("failed to decode broker response: %w", err)
	}

	return &buildResp, nil
}

// StatusCallback is invoked on every progress update
type StatusCallback func(status *BuildStatus)

// WaitForCompletion waits for the build to complete via SSE stream,
// with automatic fallback to polling every 2 seconds if SSE is unavailable.
func (b *BrokerClient) WaitForCompletion(ctx context.Context, key string, cb StatusCallback) (*BuildStatus, error) {
	// First try SSE stream
	status, err := b.streamSSE(ctx, key, cb)
	if err == nil && status != nil {
		return status, nil
	}

	// Fallback to polling every 2s
	return b.pollStatus(ctx, key, cb)
}

// streamSSE connects to SSE endpoint GET /build/:key/status
func (b *BrokerClient) streamSSE(ctx context.Context, key string, cb StatusCallback) (*BuildStatus, error) {
	streamURL := fmt.Sprintf("%s/build/%s/status?stream=true", b.BrokerURL, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// Use custom client without general timeout for long-lived SSE
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil, fmt.Errorf("server does not support SSE (status: %d)", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var latestStatus *BuildStatus

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}

			var status BuildStatus
			if err := json.Unmarshal([]byte(data), &status); err == nil {
				latestStatus = &status
				if cb != nil {
					cb(&status)
				}
				if status.Status == "completed" {
					return &status, nil
				}
				if status.Status == "failed" {
					return &status, fmt.Errorf("remote build failed: %s", status.Error)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return latestStatus, fmt.Errorf("SSE stream closed before completion")
}

// pollStatus polls GET /build/:key/status every 2 seconds
func (b *BrokerClient) pollStatus(ctx context.Context, key string, cb StatusCallback) (*BuildStatus, error) {
	statusURL := fmt.Sprintf("%s/build/%s/status", b.BrokerURL, key)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
			if err != nil {
				continue
			}
			req.Header.Set("Accept", "application/json")

			resp, err := b.HTTPClient.Do(req)
			if err != nil {
				continue
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				continue
			}

			var status BuildStatus
			if err := json.Unmarshal(body, &status); err != nil {
				continue
			}

			if cb != nil {
				cb(&status)
			}

			if status.Status == "completed" {
				return &status, nil
			}
			if status.Status == "failed" {
				return &status, fmt.Errorf("remote build failed: %s", status.Error)
			}
		}
	}
}
