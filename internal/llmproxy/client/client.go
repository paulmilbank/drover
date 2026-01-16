// Package client implements an HTTP client for the LLM proxy server
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

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

// Client is an HTTP client for the LLM proxy server
type Client struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
	timeout    time.Duration
}

// Config holds client configuration
type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// NewClient creates a new LLM proxy client
func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Minute
	}

	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		apiKey:  cfg.APIKey,
		timeout: cfg.Timeout,
	}
}

// Chat sends a non-streaming chat completion request
func (c *Client) Chat(ctx context.Context, req *llmproxy.ChatRequest) (*llmproxy.ChatResponse, error) {
	if req.Stream {
		return nil, fmt.Errorf("cannot use Chat with streaming request, use StreamChat instead")
	}

	resp, err := c.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp llmproxy.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// StreamChat sends a streaming chat completion request
func (c *Client) StreamChat(ctx context.Context, req *llmproxy.ChatRequest) (<-chan llmproxy.ChatChunk, error) {
	streamReq := *req
	streamReq.Stream = true

	resp, err := c.doRequest(ctx, &streamReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	chunkCh := make(chan llmproxy.ChatChunk, 16)

	go func() {
		defer resp.Body.Close()
		defer close(chunkCh)

		c.handleStreamingResponse(resp.Body, chunkCh)
	}()

	return chunkCh, nil
}

// ChatWithRetry sends a chat completion request with retry logic
func (c *Client) ChatWithRetry(ctx context.Context, req *llmproxy.ChatRequest, maxRetries int) (*llmproxy.ChatResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, err := c.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't retry certain errors
		if strings.Contains(err.Error(), "invalid") ||
			strings.Contains(err.Error(), "authentication") ||
			strings.Contains(err.Error(), "authorization") {
			return nil, err
		}
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// doRequest performs the HTTP request
func (c *Client) doRequest(ctx context.Context, req *llmproxy.ChatRequest) (*http.Response, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v1/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	return c.httpClient.Do(httpReq)
}

// handleStreamingResponse processes SSE events
func (c *Client) handleStreamingResponse(body io.Reader, chunkCh chan<- llmproxy.ChatChunk) {
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)

		if data == "[DONE]" {
			return
		}

		var chunk llmproxy.ChatChunk
		if err := json.NewDecoder(bytes.NewReader([]byte(data))).Decode(&chunk); err != nil {
			// Send error through channel
			chunkCh <- llmproxy.ChatChunk{
				Choices: []llmproxy.Choice{
					{
						Index: 0,
						Delta: &llmproxy.MessageDelta{
							Content: fmt.Sprintf("[Error: %v]", err),
						},
					},
				},
			}
			return
		}

		chunkCh <- chunk
	}

	if err := scanner.Err(); err != nil {
		chunkCh <- llmproxy.ChatChunk{
			Choices: []llmproxy.Choice{
				{
					Index: 0,
					Delta: &llmproxy.MessageDelta{
						Content: fmt.Sprintf("[Error: %v]", err),
					},
				},
			},
		}
	}
}

// GetModels retrieves available models from the proxy
func (c *Client) GetModels(ctx context.Context) ([]llmproxy.Model, error) {
	url := c.baseURL + "/v1/models"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// This is a simplified version - in reality we'd want to get full model info
	models := make([]llmproxy.Model, len(result.Data))
	for i, m := range result.Data {
		models[i] = llmproxy.Model{
			ID:   m.ID,
			Name: m.ID,
		}
	}

	return models, nil
}

// GetHealth checks the health of the proxy server
func (c *Client) GetHealth(ctx context.Context) (map[string]interface{}, error) {
	url := c.baseURL + "/health"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check failed: status %d", resp.StatusCode)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return health, nil
}

// GetMetrics retrieves usage metrics from the proxy
func (c *Client) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	url := c.baseURL + "/metrics"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	var metrics map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return metrics, nil
}
