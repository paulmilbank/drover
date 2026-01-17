// Package tui provides terminal user interface components for Drover
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cloud-shuttle/drover/internal/db"
)

// PlanReviewTUI is a terminal UI for reviewing and approving plans
type PlanReviewTUI struct {
	plans    []*db.Plan
	selected int
	view     viewState
	details  *db.Plan
	feedback string
	err      error
	quitting bool
	store    PlanStore
}

type viewState int

const (
	viewList viewState = iota
	viewDetails
	viewFeedback
)

// PlanStore defines the interface for plan storage operations
type PlanStore interface {
	ApprovePlan(planID string, approver string) error
	RejectPlan(planID string, feedback string) error
}

// NewPlanReviewTUI creates a new plan review TUI
func NewPlanReviewTUI(plans []*db.Plan, store PlanStore) *PlanReviewTUI {
	return &PlanReviewTUI{
		plans: plans,
		store: store,
		view:  viewList,
	}
}

// Init initializes the model
func (m *PlanReviewTUI) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *PlanReviewTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case planApprovedMsg:
		return m.handlePlanApproved(msg)
	case planRejectedMsg:
		return m.handlePlanRejected(msg)
	case errorMsg:
		m.err = msg
	}

	return m, nil
}

func (m *PlanReviewTUI) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if m.view == viewList && m.selected > 0 {
			m.selected--
		}

	case "down", "j":
		if m.view == viewList && m.selected < len(m.plans)-1 {
			m.selected++
		}

	case "enter":
		return m.handleEnter()

	case "escape":
		if m.view == viewDetails || m.view == viewFeedback {
			m.view = viewList
			m.details = nil
			m.feedback = ""
		}

	case "a":
		if m.view == viewDetails && m.details != nil {
			return m, approvePlan(m.details, m.store)
		}

	case "r", "f":
		if m.view == viewDetails && m.details != nil {
			m.view = viewFeedback
		}

	case " ":
		if m.view == viewFeedback {
			m.feedback += " "
		}

	case "backspace", "ctrl+h":
		if m.view == viewFeedback && len(m.feedback) > 0 {
			m.feedback = m.feedback[:len(m.feedback)-1]
		}

	default:
		// Handle character input for feedback
		if m.view == viewFeedback && len(msg.String()) == 1 && msg.String() >= " " && msg.String() <= "~" {
			m.feedback += msg.String()
		}
	}

	return m, nil
}

func (m *PlanReviewTUI) handleEnter() (tea.Model, tea.Cmd) {
	if m.view == viewList && len(m.plans) > 0 {
		m.view = viewDetails
		m.details = m.plans[m.selected]
	} else if m.view == viewDetails {
		// View next action based on details
		return m, nil
	} else if m.view == viewFeedback && m.details != nil {
		// Submit rejection with feedback
		return m, rejectPlan(m.details, m.feedback, m.store)
	}
	return m, nil
}

func (m *PlanReviewTUI) handlePlanApproved(msg planApprovedMsg) (tea.Model, tea.Cmd) {
	// Remove the approved plan from the list
	for i, p := range m.plans {
		if p.ID == msg.planID {
			m.plans = append(m.plans[:i], m.plans[i+1:]...)
			break
		}
	}
	if m.selected >= len(m.plans) && len(m.plans) > 0 {
		m.selected = len(m.plans) - 1
	}
	m.view = viewList
	m.details = nil
	return m, nil
}

func (m *PlanReviewTUI) handlePlanRejected(msg planRejectedMsg) (tea.Model, tea.Cmd) {
	// Remove the rejected plan from the list
	for i, p := range m.plans {
		if p.ID == msg.planID {
			m.plans = append(m.plans[:i], m.plans[i+1:]...)
			break
		}
	}
	if m.selected >= len(m.plans) && len(m.plans) > 0 {
		m.selected = len(m.plans) - 1
	}
	m.view = viewList
	m.details = nil
	m.feedback = ""
	return m, nil
}

// View renders the UI
func (m *PlanReviewTUI) View() string {
	if m.quitting {
		return ""
	}

	if m.err != nil {
		return errorStyle.Render(m.err.Error())
	}

	switch m.view {
	case viewList:
		return m.renderList()
	case viewDetails:
		return m.renderDetails()
	case viewFeedback:
		return m.renderFeedback()
	default:
		return ""
	}
}

func (m *PlanReviewTUI) renderList() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("╔════════════════════════════════════════════════════════════╗\n"))
	b.WriteString(titleStyle.Render("║           Drover Plan Review                                    ║\n"))
	b.WriteString(titleStyle.Render("╚════════════════════════════════════════════════════════════╝\n"))
	b.WriteString("\n")

	if len(m.plans) == 0 {
		b.WriteString(dimStyle.Render("No plans pending review.\n"))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("Press q to quit\n"))
		return b.String()
	}

	b.WriteString(subtitleStyle.Render("Plans Pending Approval:\n"))
	b.WriteString("\n")

	for i, plan := range m.plans {
		cursor := " "
		if i == m.selected {
			cursor = "→"
			b.WriteString(selectedStyle.Render(cursor))
		} else {
			b.WriteString(normalStyle.Render(cursor))
		}
		b.WriteString(" ")

		// Plan ID and title
		line := plan.ID + ": " + plan.Title
		if len(line) > 60 {
			line = line[:57] + "..."
		}

		// Status badge
		statusBadge := getStatusBadge(plan.Status)

		if i == m.selected {
			b.WriteString(selectedStyle.Render(line))
			b.WriteString(" ")
			b.WriteString(statusBadge)
			b.WriteString("\n")
			b.WriteString(selectedStyle.Render("  Complexity: " + plan.Complexity + " | Steps: " + strings.Trim(strings.Join(strings.Fields(fmtSteps(plan.Steps)), ", "), "[]")))
			b.WriteString("\n")
		} else {
			b.WriteString(normalStyle.Render(line))
			b.WriteString(" ")
			b.WriteString(statusBadge)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Controls:\n"))
	b.WriteString(helpStyle.Render("  ↑/k or ↓/j    Navigate plans\n"))
	b.WriteString(helpStyle.Render("  Enter          View plan details\n"))
	b.WriteString(helpStyle.Render("  q              Quit\n"))

	return b.String()
}

func (m *PlanReviewTUI) renderDetails() string {
	if m.details == nil {
		return m.renderList()
	}

	var b strings.Builder

	b.WriteString(titleStyle.Render("╔════════════════════════════════════════════════════════════╗\n"))
	b.WriteString(titleStyle.Render("║  Plan Details                                                 ║\n"))
	b.WriteString(titleStyle.Render("╚════════════════════════════════════════════════════════════╝\n"))
	b.WriteString("\n")

	// Plan info
	b.WriteString(headerStyle.Render("ID:          " + m.details.ID + "\n"))
	b.WriteString(headerStyle.Render("Task:        " + m.details.Title + "\n"))
	b.WriteString(headerStyle.Render("Status:      " + string(m.details.Status) + "\n"))
	b.WriteString(headerStyle.Render("Complexity:  " + m.details.Complexity + "\n"))
	b.WriteString(headerStyle.Render("Revision:    " + fmtInt(m.details.Revision) + "\n"))

	if m.details.ParentPlanID != "" {
		b.WriteString(headerStyle.Render("Parent:      " + m.details.ParentPlanID + "\n"))
	}

	b.WriteString("\n")

	// Description
	if m.details.Description != "" {
		b.WriteString(subtitleStyle.Render("Overview:\n"))
		b.WriteString(normalStyle.Render(wrapText(m.details.Description, 70)))
		b.WriteString("\n\n")
	}

	// Steps
	if len(m.details.Steps) > 0 {
		b.WriteString(subtitleStyle.Render("Steps:\n"))
		for _, step := range m.details.Steps {
			b.WriteString(normalStyle.Render(fmtInt(step.Order)+". "+step.Title+"\n"))
			if step.Description != "" {
				b.WriteString(dimStyle.Render("   "+wrapText(step.Description, 67)+"\n"))
			}
			if len(step.Files) > 0 {
				b.WriteString(dimStyle.Render("   Files: "+strings.Join(step.Files, ", ")+"\n"))
			}
			if step.Verification != "" {
				b.WriteString(successStyle.Render("   ✓ "+step.Verification+"\n"))
			}
		}
		b.WriteString("\n")
	}

	// Files to create
	if len(m.details.FilesToCreate) > 0 {
		b.WriteString(subtitleStyle.Render("Files to Create:\n"))
		for _, f := range m.details.FilesToCreate {
			b.WriteString(infoStyle.Render("  + "+f.Path))
			if f.Reason != "" {
				b.WriteString(dimStyle.Render(" ("+f.Reason+")"))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Files to modify
	if len(m.details.FilesToModify) > 0 {
		b.WriteString(subtitleStyle.Render("Files to Modify:\n"))
		for _, f := range m.details.FilesToModify {
			b.WriteString(warningStyle.Render("  ~ "+f.Path))
			if f.Reason != "" {
				b.WriteString(dimStyle.Render(" ("+f.Reason+")"))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Risk factors
	if len(m.details.RiskFactors) > 0 {
		b.WriteString(subtitleStyle.Render("Risk Factors:\n"))
		for _, r := range m.details.RiskFactors {
			b.WriteString(errorStyle.Render("  ⚠ "+r+"\n"))
		}
		b.WriteString("\n")
	}

	// Existing feedback
	if len(m.details.Feedback) > 0 {
		b.WriteString(subtitleStyle.Render("Previous Feedback:\n"))
		for _, f := range m.details.Feedback {
			b.WriteString(dimStyle.Render("  • "+f+"\n"))
		}
		b.WriteString("\n")
	}

	// Controls
	b.WriteString(helpStyle.Render("Controls:\n"))
	b.WriteString(helpStyle.Render("  a              Approve this plan\n"))
	b.WriteString(helpStyle.Render("  r/f            Reject this plan (with feedback)\n"))
	b.WriteString(helpStyle.Render("  Esc            Back to list\n"))
	b.WriteString(helpStyle.Render("  q              Quit\n"))

	return b.String()
}

func (m *PlanReviewTUI) renderFeedback() string {
	if m.details == nil {
		return m.renderList()
	}

	var b strings.Builder

	b.WriteString(titleStyle.Render("╔════════════════════════════════════════════════════════════╗\n"))
	b.WriteString(titleStyle.Render("║  Reject Plan - Provide Feedback                                ║\n"))
	b.WriteString(titleStyle.Render("╚════════════════════════════════════════════════════════════╝\n"))
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("Plan: " + m.details.Title + "\n"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("Enter feedback (why this plan should be rejected):\n"))
	b.WriteString("\n")

	if m.feedback != "" {
		b.WriteString(inputStyle.Render(m.feedback + "_\n"))
	} else {
		b.WriteString(inputStyle.Render("Type your feedback here..._\n"))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Controls:\n"))
	b.WriteString(helpStyle.Render("  Enter          Submit rejection with feedback\n"))
	b.WriteString(helpStyle.Render("  Backspace      Delete character\n"))
	b.WriteString(helpStyle.Render("  Esc            Cancel\n"))

	return b.String()
}

// Messages
type planApprovedMsg struct {
	planID string
}

type planRejectedMsg struct {
	planID   string
	feedback string
}

type errorMsg error

// Commands
func approvePlan(plan *db.Plan, store PlanStore) tea.Cmd {
	return func() tea.Msg {
		if err := store.ApprovePlan(plan.ID, "operator"); err != nil {
			return errorMsg(fmt.Errorf("approving plan: %w", err))
		}
		return planApprovedMsg{planID: plan.ID}
	}
}

func rejectPlan(plan *db.Plan, feedback string, store PlanStore) tea.Cmd {
	return func() tea.Msg {
		if err := store.RejectPlan(plan.ID, feedback); err != nil {
			return errorMsg(fmt.Errorf("rejecting plan: %w", err))
		}
		return planRejectedMsg{planID: plan.ID, feedback: feedback}
	}
}

// Helper functions
func fmtSteps(steps []db.PlanStep) string {
	var names []string
	for _, s := range steps {
		names = append(names, s.Title)
	}
	return strings.Join(names, ", ")
}

func fmtInt(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return fmt.Sprintf("%d", i)
}

func wrapText(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	var result strings.Builder
	lineLen := 0

	for i, word := range words {
		if lineLen+len(word) > width && lineLen > 0 {
			result.WriteString("\n")
			lineLen = 0
		} else if i > 0 {
			result.WriteString(" ")
			lineLen++
		}
		result.WriteString(word)
		lineLen += len(word)
	}

	return result.String()
}

func getStatusBadge(status db.PlanStatus) string {
	switch status {
	case db.PlanStatusDraft:
		return dimStyle.Render("[DRAFT]")
	case db.PlanStatusPending:
		return warningStyle.Render("[PENDING]")
	case db.PlanStatusApproved:
		return successStyle.Render("[APPROVED]")
	case db.PlanStatusRejected:
		return errorStyle.Render("[REJECTED]")
	case db.PlanStatusExecuting:
		return infoStyle.Render("[EXECUTING]")
	case db.PlanStatusCompleted:
		return successStyle.Render("[DONE]")
	case db.PlanStatusFailed:
		return errorStyle.Render("[FAILED]")
	default:
		return dimStyle.Render("[" + string(status) + "]")
	}
}

// Styles
var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Align(lipgloss.Center)
	subtitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("228"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229"))
	normalStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226")).Background(lipgloss.Color("235"))
	dimStyle     = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("245"))
	helpStyle    = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("243"))
	inputStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))

	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	infoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
)
