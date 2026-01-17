// Package modes implements building execution mode
package modes

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloud-shuttle/drover/internal/db"
	"github.com/cloud-shuttle/drover/pkg/types"
	"go.opentelemetry.io/otel/trace"
)

// Builder executes approved implementation plans
type Builder struct {
	config    *Config
	agent     BuildingAgent
	store     PlanStore
	verbose   bool
}

// BuildingAgent is the interface for agents that can execute plans
type BuildingAgent interface {
	// ExecutePlan executes a plan and returns the result
	ExecutePlan(ctx context.Context, plan *db.Plan, task *types.Task, worktreePath string, span ...trace.Span) *BuildResult
}

// BuildResult represents the result of building a plan
type BuildResult struct {
	Success      bool          `json:"success"`
	PlanID       string        `json:"plan_id"`
	TaskID       string        `json:"task_id"`
	StepsCompleted int         `json:"steps_completed"`
	TotalSteps   int           `json:"total_steps"`
	StepResults  []StepResult  `json:"step_results"`
	Output       string        `json:"output"`
	Error        error         `json:"error,omitempty"`
	Duration     time.Duration `json:"duration"`
	NeedsRefinement bool        `json:"needs_refinement,omitempty"`
	FailureReason   string      `json:"failure_reason,omitempty"`
}

// NewBuilder creates a new Builder
func NewBuilder(cfg *Config, agent BuildingAgent, store PlanStore) *Builder {
	return &Builder{
		config:  cfg,
		agent:   agent,
		store:   store,
		verbose: false,
	}
}

// SetVerbose enables verbose logging
func (b *Builder) SetVerbose(v bool) {
	b.verbose = v
}

// ExecutePlan executes an approved plan
func (b *Builder) ExecutePlan(ctx context.Context, plan *db.Plan, task *types.Task, worktreePath string, span ...trace.Span) *BuildResult {
	start := time.Now()

	if b.verbose {
		log.Printf("🔨 Builder: Executing plan %s for task %s", plan.ID, task.ID)
	}

	// Update plan status to executing
	plan.Status = db.PlanStatusExecuting
	plan.UpdatedAt = time.Now()
	if err := b.store.UpdatePlanStatus(plan.ID, db.PlanStatusExecuting, ""); err != nil {
		log.Printf("Error updating plan status: %v", err)
	}

	// Execute the plan using the agent
	result := b.agent.ExecutePlan(ctx, plan, task, worktreePath, span...)
	result.PlanID = plan.ID
	result.TaskID = task.ID
	result.TotalSteps = len(plan.Steps)
	result.Duration = time.Since(start)

	// Update plan status based on result
	if result.Success {
		plan.Status = db.PlanStatusCompleted
		if b.verbose {
			log.Printf("✅ Plan %s completed successfully in %v", plan.ID, result.Duration)
		}
	} else {
		if result.NeedsRefinement && b.config.Refinement.Enabled {
			plan.Status = db.PlanStatusRejected
			if b.verbose {
				log.Printf("❌ Plan %s failed, needs refinement: %s", plan.ID, result.FailureReason)
			}
		} else {
			plan.Status = db.PlanStatusFailed
			if b.verbose {
				log.Printf("❌ Plan %s failed: %v", plan.ID, result.Error)
			}
		}
	}

	plan.UpdatedAt = time.Now()
	if err := b.store.UpdatePlanStatus(plan.ID, plan.Status, result.FailureReason); err != nil {
		log.Printf("Error updating plan status: %v", err)
	}

	return result
}

// CheckPlanReady checks if a plan is ready to be executed
func (b *Builder) CheckPlanReady(plan *db.Plan) error {
	if b.config.Building.ExecuteApprovedOnly && plan.Status != db.PlanStatusApproved {
		return fmt.Errorf("plan %s is not approved (status: %s)", plan.ID, plan.Status)
	}

	if len(plan.Steps) == 0 {
		return fmt.Errorf("plan %s has no steps to execute", plan.ID)
	}

	return nil
}

// ShouldRefine determines if a failed plan should be refined
func (b *Builder) ShouldRefine(plan *db.Plan, result *BuildResult) bool {
	if !b.config.Refinement.Enabled {
		return false
	}

	if !result.NeedsRefinement && plan.Status != db.PlanStatusFailed {
		return false
	}

	// Check if we've exceeded max refinements
	if plan.Revision >= b.config.Refinement.MaxRefinements {
		if b.verbose {
			log.Printf("⚠️  Plan %s has exceeded max refinements (%d)",
				plan.ID, b.config.Refinement.MaxRefinements)
		}
		return false
	}

	// Check if the failure trigger is enabled
	for _, trigger := range b.config.Refinement.Triggers {
		if trigger == TriggerOnFailure {
			return true
		}
	}

	return false
}

// RequestRefinement creates a refinement request for a failed plan
func (b *Builder) RequestRefinement(ctx context.Context, plan *db.Plan, result *BuildResult, workerID string) (*db.Plan, error) {
	if b.verbose {
		log.Printf("🔄 Requesting refinement for plan %s", plan.ID)
	}

	// Store the failure reason as feedback
	if err := b.store.AddFeedback(plan.ID,
		fmt.Sprintf("Execution failed at step %d: %s",
			result.StepsCompleted+1, result.FailureReason)); err != nil {
		return nil, fmt.Errorf("adding feedback: %w", err)
	}

	// Create a refined plan
	refinedPlan := &db.Plan{
		ID:              fmt.Sprintf("plan_%s_%d", plan.TaskID, time.Now().UnixNano()),
		TaskID:          plan.TaskID,
		Title:           plan.Title,
		Description:     plan.Description,
		Status:          db.PlanStatusDraft,
		Revision:        plan.Revision + 1,
		ParentPlanID:    plan.ID,
		Feedback:        append([]string{}, plan.Feedback...),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		CreatedBy:       workerID,
		Steps:           []db.PlanStep{},
		FilesToCreate:   append([]db.FileSpec{}, plan.FilesToCreate...),
		FilesToModify:   append([]db.FileSpec{}, plan.FilesToModify...),
		RiskFactors:     append([]string{}, plan.RiskFactors...),
		Complexity:      plan.Complexity,
		EstimatedTime:   plan.EstimatedTime,
	}

	// Add failure as feedback
	refinedPlan.Feedback = append(refinedPlan.Feedback,
		fmt.Sprintf("Previous attempt (revision %d) failed: %s", plan.Revision, result.FailureReason))

	// Save the refined plan
	if err := b.store.SavePlan(refinedPlan); err != nil {
		return nil, fmt.Errorf("saving refined plan: %w", err)
	}

	if b.verbose {
		log.Printf("✅ Created refined plan %s (revision %d)", refinedPlan.ID, refinedPlan.Revision)
	}

	return refinedPlan, nil
}

// BuildExecutionPrompt builds the prompt for building mode
func (b *Builder) BuildExecutionPrompt(plan *db.Plan, task *types.Task) string {
	prompt := b.config.Building.PromptTemplate

	// Build step descriptions
	var stepsDesc string
	for _, step := range plan.Steps {
		stepsDesc += fmt.Sprintf("%d. **%s**\n", step.Order, step.Title)
		stepsDesc += fmt.Sprintf("   - %s\n", step.Description)
		if len(step.Files) > 0 {
			stepsDesc += fmt.Sprintf("   - Files: %s\n", formatStringSlice(step.Files))
		}
		if step.Verification != "" {
			stepsDesc += fmt.Sprintf("   - Verification: %s\n", step.Verification)
		}
	}

	// Build overview
	overview := plan.Description
	if overview == "" {
		overview = fmt.Sprintf("Implement %s with %d steps", task.Title, len(plan.Steps))
	}

	// Replace template variables
	replacements := map[string]string{
		"{{.TaskTitle}}":    task.Title,
		"{{.TaskDescription}}": task.Description,
		"{{.PlanOverview}}":  overview,
		"{{.PlanSteps}}":     stepsDesc,
	}

	for key, value := range replacements {
		prompt = strings.ReplaceAll(prompt, key, value)
	}

	return prompt
}

func formatStringSlice(slice []string) string {
	if len(slice) == 0 {
		return "none"
	}
	var quoted []string
	for _, s := range slice {
		quoted = append(quoted, fmt.Sprintf("'%s'", s))
	}
	return strings.Join(quoted, ", ")
}

// ExecutionContext creates an execution context for a plan
func (b *Builder) ExecutionContext(plan *db.Plan, task *types.Task, workDir string) *ExecutionContext {
	return &ExecutionContext{
		Plan:        plan,
		CurrentStep: 0,
		StepResults: make(map[int]StepResult),
		WorkDir:     workDir,
		Task:        task,
	}
}
