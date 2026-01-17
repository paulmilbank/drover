package provider

import (
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
	anthropicBaseURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
)

// AnthropicProvider implements the Anthropic (Claude) provider
type AnthropicProvider struct {
	*BaseProvider
	client *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(cfg llmproxy.ProviderConfig) (llmproxy.Provider, error) {
	models := map[string]llmproxy.Model{
		"claude-sonnet-4-20250514": {
			ID:          "claude-sonnet-4-20250514",
			Name:        "Claude Sonnet 4",
			Provider:    llmproxy.ProviderAnthropic,
			ContextSize: 200000,
			InputPrice:  3.00,  // $3 per 1M input tokens
			OutputPrice: 15.00, // $15 per 1M output tokens
		},
		"claude-3-5-sonnet-20241022": {
			ID:          "claude-3-5-sonnet-20241022",
			Name:        "Claude 3.5 Sonnet",
			Provider:    llmproxy.ProviderAnthropic,
			ContextSize: 200000,
			InputPrice:  3.00,
			OutputPrice: 15.00,
		},
		"claude-3-5-sonnet-20240620": {
			ID:          "claude-3-5-sonnet-20240620",
			Name:        "Claude 3.5 Sonnet (Legacy)",
			Provider:    llmproxy.ProviderAnthropic,
			ContextSize: 200000,
			InputPrice:  3.00,
			OutputPrice: 15.00,
		},
		"claude-3-5-haiku-20241022": {
			ID:          "claude-3-5-haiku-20241022",
			Name:        "Claude 3.5 Haiku",
			Provider:    llmproxy.ProviderAnthropic,
			ContextSize: 200000,
			InputPrice:  0.80,  // $0.80 per 1M input tokens
			OutputPrice: 4.00,  // $4 per 1M output tokens
		},
		"claude-3-opus-20240229": {
			ID:          "claude-3-opus-20240229",
			Name:        "Claude 3 Opus",
			Provider:    llmproxy.ProviderAnthropic,
			ContextSize: 200000,
			InputPrice:  15.00,
			OutputPrice: 75.00,
		},
	}

	return &AnthropicProvider{
		BaseProvider: &BaseProvider{
			Config:    cfg,
			ModelInfo: models,
		},
		client: &http.Client{},
	}, nil
}

// Name returns the provider name
func (a *AnthropicProvider) Name() llmproxy.ProviderType {
	return llmproxy.ProviderAnthropic
}

// Validate checks if the provider configuration is valid
func (a *AnthropicProvider) Validate() error {
	if a.Config.APIKey == "" {
		return fmt.Errorf("API key is required for Anthropic provider")
	}
	if a.Config.BaseURL != "" && !strings.HasPrefix(a.Config.BaseURL, "http") {
		return fmt.Errorf("invalid base URL for Anthropic provider")
	}
	return nil
}

// Chat generates a non-streaming chat completion
func (a *AnthropicProvider) Chat(ctx context.Context, req *llmproxy.ChatRequest) (*llmproxy.ChatResponse, error) {
	// Anthropic doesn't support non-streaming in Messages API, so we use streaming and aggregate
	chunkCh, err := a.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var toolCalls []llmproxy.ToolCall
	var usage llmproxy.Usage

	for chunk := range chunkCh {
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta != nil {
				content.WriteString(chunk.Choices[0].Delta.Content)
				if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
					toolCalls = append(toolCalls, chunk.Choices[0].Delta.ToolCalls...)
				}
			}
		}
		// Usage comes in the last chunk
		// We'll set it when we see a finish_reason
		if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != "" {
			// Estimate usage for non-streaming (not provided directly)
			usage = llmproxy.Usage{
				PromptTokens:     len(req.Messages) * 100, // Rough estimate
				CompletionTokens: len(content.String()) / 4,
				TotalTokens:      len(req.Messages)*100 + len(content.String())/4,
			}
		}
	}

	return &llmproxy.ChatResponse{
		ID:      generateID("msg"),
		Object:  "chat.completion",
		Created: 0,
		Model:   req.Model,
		Choices: []llmproxy.Choice{
			{
				Index: 0,
				Message: llmproxy.Message{
					Role:       llmproxy.RoleAssistant,
					Content:    content.String(),
					ToolCalls:  toolCalls,
				},
				FinishReason: "stop",
			},
		},
		Usage:    usage,
		Provider: llmproxy.ProviderAnthropic,
	}, nil
}

// StreamChat generates a streaming chat completion
func (a *AnthropicProvider) StreamChat(ctx context.Context, req *llmproxy.ChatRequest) (<-chan llmproxy.ChatChunk, error) {
	// Convert to Anthropic format
	anthropicReq, err := a.convertRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := anthropicBaseURL
	if a.Config.BaseURL != "" {
		baseURL = a.Config.BaseURL
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.Config.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := a.client.Do(httpReq)
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

		a.handleStreamingResponse(resp.Body, chunkCh, req.Model)
	}()

	return chunkCh, nil
}

// anthropicRequest represents Anthropic's API request format
type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	Messages  []anthropicMessage  `json:"messages"`
	System    string              `json:"system,omitempty"`
	Tools     []anthropicTool     `json:"tools,omitempty"`
	ToolChoice any                `json:"tool_choice,omitempty"`
	Stream    bool                `json:"stream"`
	Temperature float64           `json:"temperature,omitempty"`
	TopP      float64             `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string              `json:"role"`
	Content []anthropicContent  `json:"content"`
}

type anthropicContent struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// convertRequest converts a generic request to Anthropic format
func (a *AnthropicProvider) convertRequest(req *llmproxy.ChatRequest) (*anthropicRequest, error) {
	// Extract system message and build messages
	var systemPrompt string
	messages := make([]anthropicMessage, 0, len(req.Messages))

	for _, msg := range req.Messages {
		if msg.Role == llmproxy.RoleSystem {
			systemPrompt += msg.Content + "\n"
		} else {
			content := []anthropicContent{{
				Type: "text",
				Text: msg.Content,
			}}
			messages = append(messages, anthropicMessage{
				Role:    string(msg.Role),
				Content: content,
			})
		}
	}

	anthropicReq := &anthropicRequest{
		Model:         a.ApplyModelAlias(req.Model),
		MaxTokens:     4096,
		Messages:      messages,
		System:        systemPrompt,
		Stream:        true,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.Stop,
	}

	if req.MaxTokens > 0 {
		anthropicReq.MaxTokens = req.MaxTokens
	}

	// Convert tools
	if len(req.Tools) > 0 {
		anthropicReq.Tools = make([]anthropicTool, len(req.Tools))
		for i, tool := range req.Tools {
			anthropicReq.Tools[i] = anthropicTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: tool.Function.Parameters,
			}
		}
	}

	return anthropicReq, nil
}

// handleStreamingResponse processes SSE events from Anthropic
func (a *AnthropicProvider) handleStreamingResponse(body io.Reader, chunkCh chan<- llmproxy.ChatChunk, model string) {
	decoder := json.NewDecoder(body)

	for decoder.More() {
		var event map[string]json.RawMessage
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			StreamError(chunkCh, fmt.Errorf("decode error: %w", err))
			return
		}

		// Parse the event
		if eventTypeBytes, ok := event["type"]; ok {
			var eventType string
			json.Unmarshal(eventTypeBytes, &eventType)

			switch eventType {
			case "message_start":
				// Start of message - send initial chunk
				chunkCh <- llmproxy.ChatChunk{
					ID:      generateID("chunk"),
					Object:  "chat.completion.chunk",
					Created: 0,
					Model:   model,
					Choices: []llmproxy.Choice{{Index: 0}},
					Provider: llmproxy.ProviderAnthropic,
				}

			case "content_block_start", "content_block_delta":
				// Content chunks
				if deltaBytes, ok := event["delta"]; ok {
					var delta struct {
						Type  string `json:"type"`
						Text  string `json:"text"`
					}
					json.Unmarshal(deltaBytes, &delta)

					if delta.Text != "" {
						chunkCh <- llmproxy.ChatChunk{
							ID:      generateID("chunk"),
							Object:  "chat.completion.chunk",
							Created: 0,
							Model:   model,
							Choices: []llmproxy.Choice{
								{
									Index: 0,
									Delta: &llmproxy.MessageDelta{
										Content: delta.Text,
									},
								},
							},
							Provider: llmproxy.ProviderAnthropic,
						}
					}
				}

			case "message_stop":
				// End of message
				StreamDone(chunkCh)
				return

			case "error":
				if errorBytes, ok := event["error"]; ok {
					var errInfo struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					}
					json.Unmarshal(errorBytes, &errInfo)
					StreamError(chunkCh, fmt.Errorf("API error: %s", errInfo.Message))
					return
				}
			}
		}
	}
}

// GetModels returns the list of available models
func (a *AnthropicProvider) GetModels() []llmproxy.Model {
	return a.BaseProvider.GetModels()
}

// CalculateCost calculates the cost for a given usage
func (a *AnthropicProvider) CalculateCost(model string, usage *llmproxy.Usage) (*llmproxy.Cost, error) {
	return a.BaseProvider.CalculateCost(model, usage)
}

func generateID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, 0)
}
