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
	openAIBaseURL = "https://api.openai.com/v1/chat/completions"
)

// OpenAIProvider implements the OpenAI provider
type OpenAIProvider struct {
	*BaseProvider
	client *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(cfg llmproxy.ProviderConfig) (llmproxy.Provider, error) {
	models := map[string]llmproxy.Model{
		"gpt-4o": {
			ID:          "gpt-4o",
			Name:        "GPT-4o",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 128000,
			InputPrice:  2.50,  // $2.50 per 1M input tokens
			OutputPrice: 10.00, // $10 per 1M output tokens
		},
		"gpt-4o-mini": {
			ID:          "gpt-4o-mini",
			Name:        "GPT-4o Mini",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 128000,
			InputPrice:  0.15,  // $0.15 per 1M input tokens
			OutputPrice: 0.60,  // $0.60 per 1M output tokens
		},
		"o1": {
			ID:          "o1",
			Name:        "o1",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 200000,
			InputPrice:  15.00, // $15 per 1M input tokens
			OutputPrice: 60.00, // $60 per 1M output tokens
		},
		"o1-mini": {
			ID:          "o1-mini",
			Name:        "o1-mini",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 128000,
			InputPrice:  1.10,  // $1.10 per 1M input tokens
			OutputPrice: 4.40,  // $4.40 per 1M output tokens
		},
		"gpt-4-turbo": {
			ID:          "gpt-4-turbo",
			Name:        "GPT-4 Turbo",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 128000,
			InputPrice:  10.00,
			OutputPrice: 30.00,
		},
		"gpt-4": {
			ID:          "gpt-4",
			Name:        "GPT-4",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 8192,
			InputPrice:  30.00,
			OutputPrice: 60.00,
		},
		"gpt-3.5-turbo": {
			ID:          "gpt-3.5-turbo",
			Name:        "GPT-3.5 Turbo",
			Provider:    llmproxy.ProviderOpenAI,
			ContextSize: 16385,
			InputPrice:  0.50,
			OutputPrice: 1.50,
		},
	}

	return &OpenAIProvider{
		BaseProvider: &BaseProvider{
			Config:    cfg,
			ModelInfo: models,
		},
		client: &http.Client{},
	}, nil
}

// Name returns the provider name
func (o *OpenAIProvider) Name() llmproxy.ProviderType {
	return llmproxy.ProviderOpenAI
}

// Validate checks if the provider configuration is valid
func (o *OpenAIProvider) Validate() error {
	if o.Config.APIKey == "" {
		return fmt.Errorf("API key is required for OpenAI provider")
	}
	return nil
}

// Chat generates a non-streaming chat completion
func (o *OpenAIProvider) Chat(ctx context.Context, req *llmproxy.ChatRequest) (*llmproxy.ChatResponse, error) {
	// Set stream to false for non-streaming
	nonStreamReq := *req
	nonStreamReq.Stream = false

	openAIReq, err := o.convertRequest(&nonStreamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(openAIReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := openAIBaseURL
	if o.Config.BaseURL != "" {
		baseURL = o.Config.BaseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.Config.APIKey)

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(body))
	}

	var openAIResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return o.convertResponse(&openAIResp), nil
}

// StreamChat generates a streaming chat completion
func (o *OpenAIProvider) StreamChat(ctx context.Context, req *llmproxy.ChatRequest) (<-chan llmproxy.ChatChunk, error) {
	streamReq := *req
	streamReq.Stream = true

	openAIReq, err := o.convertRequest(&streamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	reqBody, err := json.Marshal(openAIReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	baseURL := openAIBaseURL
	if o.Config.BaseURL != "" {
		baseURL = o.Config.BaseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.Config.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := o.client.Do(httpReq)
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

		o.handleStreamingResponse(resp.Body, chunkCh, req.Model)
	}()

	return chunkCh, nil
}

// openAIRequest represents OpenAI's API request format
type openAIRequest struct {
	Model       string               `json:"model"`
	Messages    []openAIMessage      `json:"messages"`
	Tools       []openAITool         `json:"tools,omitempty"`
	ToolChoice  any                  `json:"tool_choice,omitempty"`
	Temperature float64              `json:"temperature,omitempty"`
	MaxTokens   int                  `json:"max_tokens,omitempty"`
	TopP        float64              `json:"top_p,omitempty"`
	Stream      bool                 `json:"stream"`
	Stop        []string             `json:"stop,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string               `json:"type"`
	Function openAIFunctionSpec   `json:"function"`
}

type openAIFunctionSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type openAIToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function openAIFunctionCall  `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIResponse represents OpenAI's API response format
type openAIResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []openAIChoice   `json:"choices"`
	Usage   openAIUsage      `json:"usage"`
}

type openAIChoice struct {
	Index        int              `json:"index"`
	Message      openAIMessage    `json:"message,omitempty"`
	Delta        *openAIMessage   `json:"delta,omitempty"`
	FinishReason string           `json:"finish_reason,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// convertRequest converts a generic request to OpenAI format
func (o *OpenAIProvider) convertRequest(req *llmproxy.ChatRequest) (*openAIRequest, error) {
	messages := make([]openAIMessage, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = openAIMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			messages[i].ToolCalls = make([]openAIToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				messages[i].ToolCalls[j] = openAIToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: openAIFunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		if msg.ToolCallID != "" {
			messages[i].ToolCallID = msg.ToolCallID
		}
	}

	openAIReq := &openAIRequest{
		Model:       o.ApplyModelAlias(req.Model),
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Stop:        req.Stop,
	}

	if len(req.Tools) > 0 {
		openAIReq.Tools = make([]openAITool, len(req.Tools))
		for i, tool := range req.Tools {
			openAIReq.Tools[i] = openAITool{
				Type: "function",
				Function: openAIFunctionSpec{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			}
		}
	}

	return openAIReq, nil
}

// convertResponse converts an OpenAI response to generic format
func (o *OpenAIProvider) convertResponse(resp *openAIResponse) *llmproxy.ChatResponse {
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
		Provider: llmproxy.ProviderOpenAI,
	}
}

// handleStreamingResponse processes SSE events from OpenAI
func (o *OpenAIProvider) handleStreamingResponse(body io.Reader, chunkCh chan<- llmproxy.ChatChunk, model string) {
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

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
				Provider: llmproxy.ProviderOpenAI,
			}
		}
	}

	if err := scanner.Err(); err != nil {
		StreamError(chunkCh, err)
	}
}

func convertToolCalls(calls []openAIToolCall) []llmproxy.ToolCall {
	result := make([]llmproxy.ToolCall, len(calls))
	for i, c := range calls {
		result[i] = llmproxy.ToolCall{
			ID:   c.ID,
			Type: c.Type,
			Function: llmproxy.FunctionCall{
				Name:      c.Function.Name,
				Arguments: c.Function.Arguments,
			},
		}
	}
	return result
}

// GetModels returns the list of available models
func (o *OpenAIProvider) GetModels() []llmproxy.Model {
	return o.BaseProvider.GetModels()
}

// CalculateCost calculates the cost for a given usage
func (o *OpenAIProvider) CalculateCost(model string, usage *llmproxy.Usage) (*llmproxy.Cost, error) {
	return o.BaseProvider.CalculateCost(model, usage)
}
