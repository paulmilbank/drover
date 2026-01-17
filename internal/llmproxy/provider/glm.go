package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

const (
	glmBaseURL = "https://open.bigmodel.cn/api/paas/v4/chat/completions"
)

// GLMProvider implements the GLM (ZhipuAI) provider
type GLMProvider struct {
	*BaseProvider
	client       *http.Client
	apiKeySecret string // Secret part of API key for JWT generation
	apiKeyID     string // ID part of API key
}

// NewGLMProvider creates a new GLM provider
func NewGLMProvider(cfg llmproxy.ProviderConfig) (llmproxy.Provider, error) {
	// GLM API key format: id.secret
	parts := strings.Split(cfg.APIKey, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid GLM API key format, expected id.secret")
	}

	models := map[string]llmproxy.Model{
		"glm-4": {
			ID:          "glm-4",
			Name:        "GLM-4",
			Provider:    llmproxy.ProviderGLM,
			ContextSize: 128000,
			InputPrice:  0.10,  // ¥0.10 per 1M input tokens (CNY)
			OutputPrice: 0.10,  // ¥0.10 per 1M output tokens
		},
		"glm-4-flash": {
			ID:          "glm-4-flash",
			Name:        "GLM-4 Flash",
			Provider:    llmproxy.ProviderGLM,
			ContextSize: 128000,
			InputPrice:  0.01,  // ¥0.01 per 1M input tokens
			OutputPrice: 0.01,  // ¥0.01 per 1M output tokens
		},
		"glm-4-air": {
			ID:          "glm-4-air",
			Name:        "GLM-4 Air",
			Provider:    llmproxy.ProviderGLM,
			ContextSize: 128000,
			InputPrice:  0.01,
			OutputPrice: 0.01,
		},
		"glm-3-turbo": {
			ID:          "glm-3-turbo",
			Name:        "GLM-3 Turbo",
			Provider:    llmproxy.ProviderGLM,
			ContextSize: 128000,
			InputPrice:  0.005,
			OutputPrice: 0.005,
		},
	}

	return &GLMProvider{
		BaseProvider: &BaseProvider{
			Config:    cfg,
			ModelInfo: models,
		},
		client:       &http.Client{},
		apiKeyID:     parts[0],
		apiKeySecret: parts[1],
	}, nil
}

// Name returns the provider name
func (g *GLMProvider) Name() llmproxy.ProviderType {
	return llmproxy.ProviderGLM
}

// Validate checks if the provider configuration is valid
func (g *GLMProvider) Validate() error {
	if g.Config.APIKey == "" {
		return fmt.Errorf("API key is required for GLM provider")
	}
	if !strings.Contains(g.Config.APIKey, ".") {
		return fmt.Errorf("invalid GLM API key format, expected id.secret")
	}
	return nil
}

// Chat generates a non-streaming chat completion
func (g *GLMProvider) Chat(ctx context.Context, req *llmproxy.ChatRequest) (*llmproxy.ChatResponse, error) {
	// GLM API is OpenAI-compatible, so we can use the OpenAI request/response format
	nonStreamReq := *req
	nonStreamReq.Stream = false

	glmReq, err := g.convertRequest(&nonStreamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(glmReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := glmBaseURL
	if g.Config.BaseURL != "" {
		baseURL = g.Config.BaseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Generate JWT token for authorization
	token, err := g.generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	var glmResp openAIResponse // GLM uses OpenAI-compatible format
	if err := json.NewDecoder(resp.Body).Decode(&glmResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return g.convertResponse(&glmResp), nil
}

// StreamChat generates a streaming chat completion
func (g *GLMProvider) StreamChat(ctx context.Context, req *llmproxy.ChatRequest) (<-chan llmproxy.ChatChunk, error) {
	streamReq := *req
	streamReq.Stream = true

	glmReq, err := g.convertRequest(&streamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(glmReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := glmBaseURL
	if g.Config.BaseURL != "" {
		baseURL = g.Config.BaseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	token, err := g.generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
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

		g.handleStreamingResponse(resp.Body, chunkCh, req.Model)
	}()

	return chunkCh, nil
}

// convertRequest converts a generic request to GLM format (OpenAI-compatible)
func (g *GLMProvider) convertRequest(req *llmproxy.ChatRequest) (*openAIRequest, error) {
	messages := make([]openAIMessage, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = openAIMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}

	glmReq := &openAIRequest{
		Model:       g.ApplyModelAlias(req.Model),
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Stop:        req.Stop,
	}

	if len(req.Tools) > 0 {
		glmReq.Tools = make([]openAITool, len(req.Tools))
		for i, tool := range req.Tools {
			glmReq.Tools[i] = openAITool{
				Type: "function",
				Function: openAIFunctionSpec{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			}
		}
	}

	return glmReq, nil
}

// convertResponse converts a GLM response to generic format
func (g *GLMProvider) convertResponse(resp *openAIResponse) *llmproxy.ChatResponse {
	choices := make([]llmproxy.Choice, len(resp.Choices))
	for i, c := range resp.Choices {
		choices[i] = llmproxy.Choice{
			Index: c.Index,
			Message: llmproxy.Message{
				Role:       llmproxy.Role(c.Message.Role),
				Content:    c.Message.Content,
				ToolCalls:  convertToolCalls(c.Message.ToolCalls),
			},
			FinishReason: c.FinishReason,
		}
	}

	return &llmproxy.ChatResponse{
		ID:       resp.ID,
		Object:   resp.Object,
		Created:  resp.Created,
		Model:    resp.Model,
		Choices:  choices,
		Usage: llmproxy.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
		Provider: llmproxy.ProviderGLM,
	}
}

// handleStreamingResponse processes SSE events from GLM
func (g *GLMProvider) handleStreamingResponse(body io.Reader, chunkCh chan<- llmproxy.ChatChunk, model string) {
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)

		if data == "[DONE]" {
			StreamDone(chunkCh)
			return
		}

		var chunk openAIResponse
		if err := json.NewDecoder(bytes.NewReader([]byte(data))).Decode(&chunk); err != nil {
			StreamError(chunkCh, fmt.Errorf("decode error: %w", err))
			return
		}

		if len(chunk.Choices) > 0 {
			choices := make([]llmproxy.Choice, len(chunk.Choices))
			for i, c := range chunk.Choices {
				delta := &llmproxy.MessageDelta{}
				if c.Delta != nil {
					delta.Role = llmproxy.Role(c.Delta.Role)
					delta.Content = c.Delta.Content
					if len(c.Delta.ToolCalls) > 0 {
						delta.ToolCalls = convertToolCalls(c.Delta.ToolCalls)
					}
				}

				choices[i] = llmproxy.Choice{
					Index:        c.Index,
					Delta:        delta,
					FinishReason: c.FinishReason,
				}
			}

			chunkCh <- llmproxy.ChatChunk{
				ID:       chunk.ID,
				Object:   chunk.Object,
				Created:  chunk.Created,
				Model:    model,
				Choices:  choices,
				Provider: llmproxy.ProviderGLM,
			}
		}
	}

	if err := scanner.Err(); err != nil {
		StreamError(chunkCh, err)
	}
}

// generateToken generates a JWT token for GLM API authentication
// GLM uses a custom JWT format with HMAC-SHA256
func (g *GLMProvider) generateToken() (string, error) {
	now := time.Now()
	exp := now.Add(1 * time.Hour)

	// Create JWT header
	header := map[string]string{
		"alg": "HS256",
		"sign_type": "SIGN",
	}

	// Create JWT payload
	payload := map[string]interface{}{
		"api_key":   g.apiKeyID,
		"exp":       exp.Unix(),
		"timestamp": now.Unix(),
	}

	// Encode header
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerBytes)

	// Encode payload
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// Create signature
	message := headerEncoded + "." + payloadEncoded
	sig := hmac.New(sha256.New, []byte(g.apiKeySecret))
	sig.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))

	// Combine to form JWT
	token := message + "." + signature
	return token, nil
}

// GetModels returns the list of available models
func (g *GLMProvider) GetModels() []llmproxy.Model {
	return g.BaseProvider.GetModels()
}

// CalculateCost calculates the cost for a given usage
func (g *GLMProvider) CalculateCost(model string, usage *llmproxy.Usage) (*llmproxy.Cost, error) {
	cost, err := g.BaseProvider.CalculateCost(model, usage)
	if err != nil {
		return nil, err
	}
	// GLM uses CNY, but we can report as USD or keep original
	cost.Currency = "CNY"
	return cost, nil
}
