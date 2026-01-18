// Package spec provides types and functions for generating epics and tasks from design specifications
package spec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	llmclient "github.com/cloud-shuttle/drover/internal/llmproxy/client"
	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

// Analyzer uses AI to break down specs into epics and tasks
type Analyzer struct {
	client  *llmclient.Client
	model   string
	apiKey  string
	useDirectAPI bool
}

// NewAnalyzer creates a new spec analyzer
func NewAnalyzer(llmClient *llmclient.Client, model string) *Analyzer {
	return &Analyzer{
		client: llmClient,
		model:  model,
		useDirectAPI: false,
	}
}

// NewAnalyzerWithDirectAPI creates a new spec analyzer that uses Anthropic API directly
func NewAnalyzerWithDirectAPI(apiKey, model string) *Analyzer {
	return &Analyzer{
		apiKey: apiKey,
		model:  model,
		useDirectAPI: true,
	}
}

// AnalyzeSpec analyzes design content and generates epics/tasks
func (a *Analyzer) AnalyzeSpec(ctx context.Context, content string) (*SpecAnalysis, error) {
	prompt := a.buildPrompt(content)

	var responseContent string
	var err error

	if a.useDirectAPI {
		responseContent, err = a.callAnthropicDirect(ctx, prompt)
	} else {
		responseContent, err = a.callViaProxy(ctx, prompt)
	}

	if err != nil {
		return nil, err
	}

	// Extract JSON from response
	jsonStr, err := a.extractJSON(responseContent)
	if err != nil {
		return nil, fmt.Errorf("extracting JSON: %w", err)
	}

	// Parse the structured response
	var analysis SpecAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, fmt.Errorf("parsing AI response: %w (raw JSON: %s)", err, jsonStr)
	}

	// Validate the analysis
	if err := a.validateAnalysis(&analysis); err != nil {
		return nil, fmt.Errorf("invalid analysis: %w", err)
	}

	return &analysis, nil
}

// callViaProxy calls the LLM through the proxy server
func (a *Analyzer) callViaProxy(ctx context.Context, prompt string) (string, error) {
	req := &llmproxy.ChatRequest{
		Model: a.model,
		Messages: []llmproxy.Message{
			{
				Role:    llmproxy.RoleSystem,
				Content: "You are an expert project manager and technical lead. You break down design specifications into actionable epics, stories, and tasks.",
			},
			{
				Role:    llmproxy.RoleUser,
				Content: prompt,
			},
		},
		Temperature: 0.3,
		MaxTokens:   8000,
	}

	resp, err := a.client.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("calling AI via proxy: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from AI")
	}

	return resp.Choices[0].Message.Content, nil
}

// callAnthropicDirect calls the Anthropic API directly
func (a *Analyzer) callAnthropicDirect(ctx context.Context, prompt string) (string, error) {
	// Anthropic API request body
	type AnthropicMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type AnthropicRequest struct {
		Model     string             `json:"model"`
		MaxTokens int                `json:"max_tokens"`
		Messages  []AnthropicMessage `json:"messages"`
		System    string             `json:"system,omitempty"`
	}

	reqBody := AnthropicRequest{
		Model:     a.model,
		MaxTokens: 8000,
		Messages: []AnthropicMessage{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		System: "You are an expert project manager and technical lead. You break down design specifications into actionable epics, stories, and tasks.",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Anthropic API error: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return result.Content[0].Text, nil
}

// buildPrompt creates the prompt for AI analysis
func (a *Analyzer) buildPrompt(content string) string {
	return fmt.Sprintf(`You are analyzing design specifications to create a structured project plan with epics, stories (tasks), and subtasks.

## Design Specification

%s

## Your Task

Analyze this design specification and break it down into:

1. **Epics**: High-level features or major initiatives
2. **Stories (Tasks)**: User-facing work items within each epic
3. **Subtasks**: Technical implementation steps for each story

## Requirements

1. **Epic Structure**:
   - Each epic should represent a major feature or initiative
   - Title should be clear and descriptive
   - Description should explain the "why" and business value

2. **Story (Task) Structure**:
   - Each story should be a user-facing deliverable
   - Must have acceptance criteria (how to verify it works)
   - Should specify test mode (strict/lenient/disabled)
   - Should specify test scope (all/diff/skip)
   - Priority: 1-10 (higher = more urgent)
   - Type: feature, bug, refactor, test, docs, research, fix, other

3. **Subtask Structure**:
   - Break down stories into 2-6 technical implementation steps
   - Each subtask should be completable in 1-2 hours
   - Subtasks should be sequenced logically

4. **Quality Standards**:
   - Titles must start with action verbs (Create, Fix, Add, Update, Implement, Refactor)
   - Descriptions must mention specific files, components, or packages
   - Acceptance criteria must be testable and specific
   - Avoid vague phrases like "various improvements", "make it better"

## Output Format

Respond ONLY with valid JSON in this exact format:

{
  "epics": [
    {
      "title": "Epic title here",
      "description": "Detailed epic description",
      "tasks": [
        {
          "title": "Story title",
          "description": "Detailed story description with file paths and technical details",
          "type": "feature",
          "priority": 5,
          "acceptance_criteria": [
            "Specific criterion 1",
            "Specific criterion 2"
          ],
          "test_mode": "strict",
          "test_scope": "diff",
          "sub_tasks": [
            {
              "title": "Subtask title",
              "description": "What this subtask implements",
              "priority": 3
            }
          ],
          "blocked_by": ["0.0"]
        }
      ]
    }
  ]
}

## Guidelines

- Create 2-5 epics from this specification
- Each epic should have 3-8 stories
- Each story should have 2-6 subtasks if needed
- Use "strict" test_mode for critical features
- Use "lenient" test_mode for non-critical work
- Use "disabled" test_mode for documentation/research
- Set appropriate task dependencies using blocked_by (format: "epicIndex.taskIndex", e.g., "0.0", "0.1")
- Be specific about files, components, and technical details

Begin your analysis now.`, content)
}

// extractJSON extracts JSON from AI response (handles markdown code blocks)
func (a *Analyzer) extractJSON(content string) (string, error) {
	// Try to extract JSON from markdown code blocks
	re := regexp.MustCompile("```json\n([\\s\\S]*?)\n?```")
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), nil
	}

	// Try without language specifier
	re = regexp.MustCompile("```\n([\\s\\S]*?)\n?```")
	matches = re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), nil
	}

	// Try to find raw JSON object
	re = regexp.MustCompile(`\{[\s\S]*\}`)
	matches = re.FindStringSubmatch(content)
	if len(matches) > 0 {
		return strings.TrimSpace(matches[0]), nil
	}

	return "", fmt.Errorf("no JSON found in response")
}

// validateAnalysis validates the AI-generated analysis
func (a *Analyzer) validateAnalysis(analysis *SpecAnalysis) error {
	if len(analysis.Epics) == 0 {
		return fmt.Errorf("no epics generated")
	}

	for i, epic := range analysis.Epics {
		if strings.TrimSpace(epic.Title) == "" {
			return fmt.Errorf("epic %d: missing title", i)
		}
		if len(epic.Tasks) == 0 {
			return fmt.Errorf("epic %d (%s): no tasks", i, epic.Title)
		}

		for j, task := range epic.Tasks {
			if strings.TrimSpace(task.Title) == "" {
				return fmt.Errorf("epic %d, task %d: missing title", i, j)
			}
			if len(task.AcceptanceCriteria) == 0 {
				return fmt.Errorf("epic %d, task %d (%s): no acceptance criteria", i, j, task.Title)
			}
		}
	}

	return nil
}
