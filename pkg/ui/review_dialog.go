package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ReviewAction represents the type of review action
type ReviewAction string

const (
	ReviewActionComment        ReviewAction = "COMMENT"
	ReviewActionApprove        ReviewAction = "APPROVE"
	ReviewActionRequestChanges ReviewAction = "REQUEST_CHANGES"
	ReviewActionCancel         ReviewAction = "CANCEL"
)

// ReviewResult contains the result of the review dialog
type ReviewResult struct {
	Action ReviewAction
	Body   string
}

type focusField int

const (
	focusTextarea focusField = iota
	focusRequestChanges
	focusApprove
	focusComment
)

// ReviewDialogModel is the Bubble Tea model for the review dialog
type ReviewDialogModel struct {
	textarea    textarea.Model
	focus       focusField
	windowWidth int
	Result      *ReviewResult
}

// NewReviewDialog creates a new review dialog model
func NewReviewDialog() *ReviewDialogModel {
	ta := textarea.New()
	ta.Placeholder = "Leave a comment, be kind"
	ta.Focus()
	ta.CharLimit = 2000
	ta.SetHeight(10)
	ta.ShowLineNumbers = false

	return &ReviewDialogModel{
		textarea: ta,
		focus:    focusTextarea,
	}
}

func (m *ReviewDialogModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m *ReviewDialogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.Result = &ReviewResult{Action: ReviewActionCancel}
			return m, tea.Quit

		case "tab":
			m.cycleFocus(true)
			return m, nil

		case "shift+tab":
			m.cycleFocus(false)
			return m, nil

		case "enter":
			if m.focus != focusTextarea {
				// Trigger button action
				body := strings.TrimSpace(m.textarea.Value())
				if body == "" {
					// Don't submit empty reviews
					return m, nil
				}

				var action ReviewAction
				switch m.focus {
				case focusRequestChanges:
					action = ReviewActionRequestChanges
				case focusApprove:
					action = ReviewActionApprove
				case focusComment:
					action = ReviewActionComment
				}

				m.Result = &ReviewResult{
					Action: action,
					Body:   body,
				}
				return m, tea.Quit
			}
			// If in textarea, enter just adds a newline (handled by textarea.Update)
		}
	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.textarea.SetWidth(msg.Width - 4)
	}

	// Update textarea if focused
	if m.focus == focusTextarea {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *ReviewDialogModel) cycleFocus(next bool) {
	if next {
		m.focus++
		if m.focus > focusComment {
			m.focus = focusTextarea
		}
	} else {
		m.focus--
		if m.focus < focusTextarea {
			m.focus = focusComment
		}
	}

	if m.focus == focusTextarea {
		m.textarea.Focus()
	} else {
		m.textarea.Blur()
	}
}

func (m *ReviewDialogModel) View() string {
	var b strings.Builder

	// Header
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	b.WriteString(titleStyle.Render("Finish your review"))
	b.WriteString("\n\n")

	// Textarea
	b.WriteString(m.textarea.View())
	b.WriteString("\n\n")

	// Buttons
	btnStyle := lipgloss.NewStyle().
		Padding(0, 1).
		MarginRight(1).
		Border(lipgloss.RoundedBorder())

	activeBtnStyle := btnStyle.
		BorderForeground(lipgloss.Color("62")).
		Foreground(lipgloss.Color("230"))

	// Request Changes Button
	rcBtn := "Request changes"
	if m.focus == focusRequestChanges {
		rcBtn = activeBtnStyle.Render(rcBtn)
	} else {
		rcBtn = btnStyle.Render(rcBtn)
	}

	// Approve Button
	approveBtn := "Approve"
	if m.focus == focusApprove {
		approveBtn = activeBtnStyle.Render(approveBtn)
	} else {
		approveBtn = btnStyle.Render(approveBtn)
	}

	// Comment Button
	commentBtn := "Comment"
	if m.focus == focusComment {
		commentBtn = activeBtnStyle.Render(commentBtn)
	} else {
		commentBtn = btnStyle.Render(commentBtn)
	}

	// Helper text
	helperStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).MarginTop(1)
	helper := helperStyle.Render("tab: navigate • enter: submit • esc: cancel")

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, rcBtn, approveBtn, commentBtn))
	b.WriteString("\n")
	b.WriteString(helper)

	// Center the dialog
	docStyle := lipgloss.NewStyle().Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62"))
	return docStyle.Render(b.String())
}

// GetReviewInput launches the review dialog and returns the result
func GetReviewInput() (*ReviewResult, error) {
	m := NewReviewDialog()
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	dialogModel := finalModel.(*ReviewDialogModel)
	if dialogModel.Result == nil {
		return nil, fmt.Errorf("cancelled")
	}

	if dialogModel.Result.Action == ReviewActionCancel {
		return nil, fmt.Errorf("cancelled")
	}

	return dialogModel.Result, nil
}
