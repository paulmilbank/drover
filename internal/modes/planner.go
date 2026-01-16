// Package modes implements planning and building worker modes
package modes

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/cloud-shuttle/drover/internal/db"
	"github.com/cloud-shuttle/drover/pkg/types"
)

// Planner creates implementation plans without making code changes
type Planner struct {
	config    *Config
	agent     PlanningAgent
	store     PlanStore
	verbose   bool
}

// PlanningAgent is the interface for agents that can create plans
type PlanningAgent interface {
	// GeneratePlan creates a plan from a task
	GeneratePlan(ctx context.Context, task *types.Task, prompt string) (*db.Plan, error)
}

// PlanStore defines the interface for storing and retrieving plans
type PlanStore interface {
	// SavePlan saves a plan to storage
	SavePlan(plan *db.Plan) error

	// GetPlan retrieves a plan by ID
	GetPlan(planID string) (*db.Plan, error)

	// GetPlanByTaskID retrieves a plan for a specific task
	GetPlanByTaskID(taskID string) (*db.Plan, error)

	// ListPlans lists all plans, optionally filtered by status
	ListPlans(status db.PlanStatus) ([]*db.Plan, error)

	// UpdatePlanStatus updates the status of a plan
	UpdatePlanStatus(planID string, status db.PlanStatus, reason string) error

	// AddFeedback adds feedback to a plan
	AddFeedback(planID string, feedback string) error
}

// NewPlanner creates a new Planner
func NewPlanner(cfg *Config, agent PlanningAgent, store PlanStore) *Planner {
	return &Planner{
		config:  cfg,
		agent:   agent,
		store:   store,
		verbose: false,
	}
}

// SetVerbose enables verbose logging
func (p *Planner) SetVerbose(v bool) {
	p.verbose = v
}

// CreatePlan generates a plan for the given task
func (p *Planner) CreatePlan(ctx context.Context, task *types.Task, workerID string) (*db.Plan, error) {
	if p.verbose {
		log.Printf("📋 Planner: Creating plan for task %s: %s", task.ID, task.Title)
	}

	// Build the planning prompt
	prompt := p.buildPlanningPrompt(task)

	// Generate the plan using the agent
	plan, err := p.agent.GeneratePlan(ctx, task, prompt)
	if err != nil {
		return nil, fmt.Errorf("generating plan: %w", err)
	}

	// Set plan metadata
	plan.ID = fmt.Sprintf("plan_%s_%d", task.ID, time.Now().UnixNano())
	plan.TaskID = task.ID
	plan.Title = task.Title
	plan.Description = task.Description
	plan.Status = db.PlanStatusDraft
	plan.CreatedAt = time.Now()
	plan.UpdatedAt = time.Now()
	plan.CreatedBy = workerID
	plan.Revision = 1

	// Auto-approve low complexity plans if configured
	if p.config.Planning.AutoApproveLowComplexity && plan.Complexity == "low" {
		plan.Status = db.PlanStatusApproved
		now := time.Now()
		plan.ApprovedAt = &now
		plan.ApprovedBy = "system"
		if p.verbose {
			log.Printf("✅ Auto-approved low-complexity plan %s", plan.ID)
		}
	} else if p.config.Planning.RequireApproval {
		plan.Status = db.PlanStatusPending
		if p.verbose {
			log.Printf("📤 Plan %s submitted for approval", plan.ID)
		}
	} else {
		plan.Status = db.PlanStatusApproved
		now := time.Now()
		plan.ApprovedAt = &now
		plan.ApprovedBy = "system"
	}

	// Save the plan
	if err := p.store.SavePlan(plan); err != nil {
		return nil, fmt.Errorf("saving plan: %w", err)
	}

	if p.verbose {
		log.Printf("✅ Plan %s created with %d steps (status: %s)",
			plan.ID, len(plan.Steps), plan.Status)
	}

	return plan, nil
}

// buildPlanningPrompt builds the prompt for planning mode
func (p *Planner) buildPlanningPrompt(task *types.Task) string {
	prompt := p.config.Planning.PromptTemplate

	// Replace template variables
	replacements := map[string]string{
		"{{.TaskTitle}}":       task.Title,
		"{{.TaskDescription}}": task.Description,
		"{{.EpicID}}":          task.EpicID,
		"{{.MaxSteps}}":        fmt.Sprintf("%d", p.config.Planning.MaxStepsPerPlan),
	}

	for key, value := range replacements {
		prompt = strings.ReplaceAll(prompt, key, value)
	}

	return prompt
}

// ParsePlanFromOutput parses a plan from agent output
func ParsePlanFromOutput(output string) (*db.Plan, error) {
	plan := &db.Plan{
		Steps:         []db.PlanStep{},
		FilesToCreate: []db.FileSpec{},
		FilesToModify: []db.FileSpec{},
		RiskFactors:   []string{},
	}

	// Extract overview
	overviewRegex := regexp.MustCompile(`## Overview\s*\n+(.+?)(?:\n+##|\n*$)`)
	if matches := overviewRegex.FindStringSubmatch(output); len(matches) > 1 {
		plan.Description = strings.TrimSpace(matches[1])
	}

	// Extract steps
	stepRegex := regexp.MustCompile(`(\d+)\.\s+\*\*(.+?)\*\*\s*\n(?:((?:\s+-[^\n]+\n)+))`)
	stepMatches := stepRegex.FindAllStringSubmatch(output, -1)

	for _, match := range stepMatches {
		if len(match) < 3 {
			continue
		}

		step := db.PlanStep{
			Order:       len(plan.Steps) + 1,
			Title:       strings.TrimSpace(match[2]),
			Description: "",
		}

		// Parse step details
		details := match[3]
		for _, line := range strings.Split(details, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- Description:") {
				step.Description = strings.TrimSpace(strings.TrimPrefix(line, "- Description:"))
			} else if strings.HasPrefix(line, "- Files:") {
				files := strings.TrimSpace(strings.TrimPrefix(line, "- Files:"))
				step.Files = parseFileList(files)
			} else if strings.HasPrefix(line, "- Dependencies:") {
				deps := strings.TrimSpace(strings.TrimPrefix(line, "- Dependencies:"))
				step.Dependencies = parseDependencies(deps)
			} else if strings.HasPrefix(line, "- Verification:") {
				step.Verification = strings.TrimSpace(strings.TrimPrefix(line, "- Verification:"))
			} else if strings.HasPrefix(line, "- Estimated Time:") {
				timeStr := strings.TrimSpace(strings.TrimPrefix(line, "- Estimated Time:"))
				step.EstimatedTime = parseDuration(timeStr)
			}
		}

		plan.Steps = append(plan.Steps, step)
	}

	// Extract files to create
	createRegex := regexp.MustCompile(`## Files to Create\s*\n((?:-.+\n)*)`)
	if matches := createRegex.FindStringSubmatch(output); len(matches) > 1 {
		plan.FilesToCreate = parseFileSpecs(matches[1], "create")
	}

	// Extract files to modify
	modifyRegex := regexp.MustCompile(`## Files to Modify\s*\n((?:-.+\n)*)`)
	if matches := modifyRegex.FindStringSubmatch(output); len(matches) > 1 {
		plan.FilesToModify = parseFileSpecs(matches[1], "modify")
	}

	// Extract risk factors
	riskRegex := regexp.MustCompile(`## Risk Factors\s*\n((?:-.+\n)*)`)
	if matches := riskRegex.FindStringSubmatch(output); len(matches) > 1 {
		for _, line := range strings.Split(matches[1], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "-") {
				plan.RiskFactors = append(plan.RiskFactors, strings.TrimSpace(strings.TrimPrefix(line, "-")))
			}
		}
	}

	// Extract complexity
	complexityRegex := regexp.MustCompile(`## Complexity\s*\n(low|medium|high)`)
	if matches := complexityRegex.FindStringSubmatch(output); len(matches) > 1 {
		plan.Complexity = matches[1]
	} else {
		plan.Complexity = "medium" // Default
	}

	return plan, nil
}

func parseFileList(files string) []string {
	var result []string
	parts := strings.Split(files, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseDependencies(deps string) []int {
	var result []int
	parts := strings.Split(deps, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			var d int
			if _, err := fmt.Sscanf(p, "%d", &d); err == nil {
				result = append(result, d)
			}
		}
	}
	return result
}

func parseFileSpecs(input, operation string) []db.FileSpec {
	var specs []db.FileSpec
	lines := strings.Split(input, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))

		// Parse: `path/to/file`: reason
		parts := strings.SplitN(line, ":", 2)
		if len(parts) >= 1 {
			path := strings.Trim(strings.TrimSpace(parts[0]), "`'")
			reason := ""
			if len(parts) > 1 {
				reason = strings.TrimSpace(parts[1])
			}
			specs = append(specs, db.FileSpec{
				Path:      path,
				Operation: operation,
				Reason:    reason,
			})
		}
	}
	return specs
}

func parseDuration(s string) time.Duration {
	// Simple duration parsing like "5 minutes", "1 hour"
	s = strings.ToLower(s)
	if strings.Contains(s, "minute") || strings.Contains(s, "min") {
		var min int
		fmt.Sscanf(s, "%d", &min)
		return time.Duration(min) * time.Minute
	}
	if strings.Contains(s, "hour") || strings.Contains(s, "hr") {
		var hr int
		fmt.Sscanf(s, "%d", &hr)
		return time.Duration(hr) * time.Hour
	}
	return 5 * time.Minute // Default
}

// FormatPlanForDisplay formats a plan for human display
func FormatPlanForDisplay(plan *db.Plan) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("\n╔════════════════════════════════════════════════════════════╗\n"))
	sb.WriteString(fmt.Sprintf("║  Implementation Plan: %s\n", plan.ID))
	sb.WriteString(fmt.Sprintf("╠════════════════════════════════════════════════════════════╣\n"))
	sb.WriteString(fmt.Sprintf("║  Task: %s\n", plan.Title))
	if plan.Description != "" {
		sb.WriteString(fmt.Sprintf("║  %s\n", plan.Description))
	}
	sb.WriteString(fmt.Sprintf("╠════════════════════════════════════════════════════════════╣\n"))
	sb.WriteString(fmt.Sprintf("║  Status: %s | Complexity: %s | Steps: %d\n", plan.Status, plan.Complexity, len(plan.Steps)))
	sb.WriteString(fmt.Sprintf("╠────────────────────────────────────────────────────────────────╣\n"))

	for i, step := range plan.Steps {
		sb.WriteString(fmt.Sprintf("║  Step %d: %s\n", i+1, step.Title))
		sb.WriteString(fmt.Sprintf("║    %s\n", step.Description))
		if len(step.Files) > 0 {
			sb.WriteString(fmt.Sprintf("║    Files: %s\n", strings.Join(step.Files, ", ")))
		}
		if step.Verification != "" {
			sb.WriteString(fmt.Sprintf("║    Verify: %s\n", step.Verification))
		}
		sb.WriteString(fmt.Sprintf("║    \n"))
	}

	if len(plan.FilesToCreate) > 0 {
		sb.WriteString(fmt.Sprintf("╠────────────────────────────────────────────────────────────────╣\n"))
		sb.WriteString(fmt.Sprintf("║  Files to Create:\n"))
		for _, f := range plan.FilesToCreate {
			sb.WriteString(fmt.Sprintf("║    + %s (%s)\n", f.Path, f.Reason))
		}
	}

	if len(plan.FilesToModify) > 0 {
		sb.WriteString(fmt.Sprintf("╠────────────────────────────────────────────────────────────────╣\n"))
		sb.WriteString(fmt.Sprintf("║  Files to Modify:\n"))
		for _, f := range plan.FilesToModify {
			sb.WriteString(fmt.Sprintf("║    ~ %s (%s)\n", f.Path, f.Reason))
		}
	}

	if len(plan.RiskFactors) > 0 {
		sb.WriteString(fmt.Sprintf("╠────────────────────────────────────────────────────────────────╣\n"))
		sb.WriteString(fmt.Sprintf("║  Risk Factors:\n"))
		for _, r := range plan.RiskFactors {
			sb.WriteString(fmt.Sprintf("║    ⚠️  %s\n", r))
		}
	}

	sb.WriteString(fmt.Sprintf("╚════════════════════════════════════════════════════════════╝\n"))

	return sb.String()
}

// PlanToJSON converts a plan to JSON for storage
func PlanToJSON(plan *db.Plan) (string, error) {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// JSONToPlan converts JSON to a plan
func JSONToPlan(data string) (*db.Plan, error) {
	var plan db.Plan
	if err := json.Unmarshal([]byte(data), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
