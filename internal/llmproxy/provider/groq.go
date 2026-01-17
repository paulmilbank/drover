package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

const (
	groqBaseURL = "https://api.groq.com/openai/v1/chat/completions"
)

// GroqProvider implements the Groq provider
type GroqProvider struct {
	*BaseProvider
	client *http.Client
}

// NewGroqProvider creates a new Groq provider
func NewGroqProvider(cfg llmproxy.ProviderConfig) (llmproxy.Provider, error) {
	models := map[string]llmproxy.Model{
		"llama-3.3-70b-versatile": {
			ID:          "llama-3.3-70b-versatile",
			Name:        "Llama 3.3 70B Versatile",
			Provider:    llmproxy.ProviderGroq,
			ContextSize: 128000,
			InputPrice:  0.00,  // Free tier
			OutputPrice: 0.00,
		},
		"llama-3.1-70b-versatile": {
			ID:          "llama-3.1-70b-versatile",
			Name:        "Llama 3.1 70B Versatile",
			Provider:    llmproxy.ProviderGroq,
			ContextSize: 128000,
			InputPrice:  0.00,
			OutputPrice: 0.00,
		},
		"llama-3.1-8b-instant": {
			ID:          "llama-3.1-8b-instant",
			Name:        "Llama 3.1 8B Instant",
			Provider:    llmproxy.ProviderGroq,
			ContextSize: 128000,
			InputPrice:  0.00,
			OutputPrice: 0.00,
		},
		"mixtral-8x7b-32768": {
			ID:          "mixtral-8x7b-32768",
			Name:        "Mixtral 8x7b",
			Provider:    llmproxy.ProviderGroq,
			ContextSize: 32768,
			InputPrice:  0.00,
			OutputPrice: 0.00,
		},
		"gemma2-9b-it": {
			ID:          "gemma2-9b-it",
			Name:        "Gemma 2 9B",
			Provider:    llmproxy.ProviderGroq,
			ContextSize: 8192,
			InputPrice:  0.00,
			OutputPrice: 0.00,
		},
	}

	return &GroqProvider{
		BaseProvider: &BaseProvider{
			Config:    cfg,
			ModelInfo: models,
		},
		client: &http.Client{},
	}, nil
}

// Name returns the provider name
func (g *GroqProvider) Name() llmproxy.ProviderType {
	return llmproxy.ProviderGroq
}

// Validate checks if the provider configuration is valid
func (g *GroqProvider) Validate() error {
	if g.Config.APIKey == "" {
		return fmt.Errorf("API key is required for Groq provider")
	}
	return nil
}

// Chat generates a non-streaming chat completion
func (g *GroqProvider) Chat(ctx context.Context, req *llmproxy.ChatRequest) (*llmproxy.ChatResponse, error) {
	nonStreamReq := *req
	nonStreamReq.Stream = false

	groqReq, err := g.convertRequest(&nonStreamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(groqReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := groqBaseURL
	if g.Config.BaseURL != "" {
		baseURL = g.Config.BaseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.Config.APIKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	var groqResp openAIResponse // Groq uses OpenAI-compatible format
	if err := json.NewDecoder(resp.Body).Decode(&groqResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return g.convertResponse(&groqResp), nil
}

// StreamChat generates a streaming chat completion
func (g *GroqProvider) StreamChat(ctx context.Context, req *llmproxy.ChatRequest) (<-chan llmproxy.ChatChunk, error) {
	streamReq := *req
	streamReq.Stream = true

	groqReq, err := g.convertRequest(&streamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(groqReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := groqBaseURL
	if g.Config.BaseURL != "" {
		baseURL = g.Config.BaseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.Config.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

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

// convertRequest converts a generic request to Groq format (OpenAI-compatible)
func (g *GroqProvider) convertRequest(req *llmproxy.ChatRequest) (*openAIRequest, error) {
	messages := make([]openAIMessage, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = openAIMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}

	groqReq := &openAIRequest{
		Model:       g.ApplyModelAlias(req.Model),
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Stop:        req.Stop,
	}

	if len(req.Tools) > 0 {
		groqReq.Tools = make([]openAITool, len(req.Tools))
		for i, tool := range req.Tools {
			groqReq.Tools[i] = openAITool{
				Type: "function",
				Function: openAIFunctionSpec{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			}
		}
	}

	return groqReq, nil
}

// convertResponse converts a Groq response to generic format
func (g *GroqProvider) convertResponse(resp *openAIResponse) *llmproxy.ChatResponse {
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
		Provider: llmproxy.ProviderGroq,
	}
}

// handleStreamingResponse processes SSE events from Groq
func (g *GroqProvider) handleStreamingResponse(body io.Reader, chunkCh chan<- llmproxy.ChatChunk, model string) {
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
				Provider: llmproxy.ProviderGroq,
			}
		}
	}

	if err := scanner.Err(); err != nil {
		StreamError(chunkCh, err)
	}
}

// GetModels returns the list of available models
func (g *GroqProvider) GetModels() []llmproxy.Model {
	return g.BaseProvider.GetModels()
}

// CalculateCost calculates the cost for a given usage
func (g *GroqProvider) CalculateCost(model string, usage *llmproxy.Usage) (*llmproxy.Cost, error) {
	// Groq is currently free, but we return zero costs
	cost := &llmproxy.Cost{
		InputCost:    0,
		OutputCost:   0,
		TotalCost:    0,
		Currency:     "USD",
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
		Model:        model,
		Provider:     llmproxy.ProviderGroq,
	}
	return cost, nil
}
