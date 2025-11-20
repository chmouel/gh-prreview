package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ItemRenderer defines how to render an item in the selector
type ItemRenderer[T any] interface {
	// Title returns the primary display text for an item
	Title(item T) string
	// Description returns secondary text for an item
	Description(item T) string
	// Preview returns detailed preview text for an item
	Preview(item T) string
	// EditPath returns the file path to open in editor (optional)
	EditPath(item T) string
	// EditLine returns the line number to go to in editor (1-based, optional)
	EditLine(item T) int
	// FilterValue returns the string to match against when filtering
	FilterValue(item T) string
	// IsSkippable returns true if the item should be skipped during navigation
	IsSkippable(item T) bool
}

// CustomAction is a function that handles custom actions on items
type CustomAction[T any] func(item T) (string, error)

var ErrNoSelection = errors.New("no selection made")

// SelectionModel is the tea.Model for interactive selection
type SelectionModel[T any] struct {
	list         list.Model
	items        []T
	result       []T
	windowSize   tea.WindowSizeMsg
	customAction CustomAction[T]
	actionKey    string // Key binding description for custom action (e.g., "r resolve")
	onOpen       CustomAction[T]
	viewport     viewport.Model
	showDetail   bool
	filterFunc   func(T, bool) bool
	filterActive bool
	renderer     ItemRenderer[T]
	showHelp     bool
	onSelect     CustomAction[T]
}

// Item wraps a generic item for the list model
type listItem[T any] struct {
	value T
	item  ItemRenderer[T]
}

func (i listItem[T]) FilterValue() string {
	return i.item.FilterValue(i.value)
}

func (i listItem[T]) Title() string {
	return i.item.Title(i.value)
}

func (i listItem[T]) Description() string {
	return i.item.Description(i.value)
}

// SelectFromList creates an interactive selector for a list of items
// Returns selected items in order they were selected
func SelectFromList[T any](items []T, renderer ItemRenderer[T]) (T, error) {
	return SelectFromListWithAction(items, renderer, nil, "", nil, nil, nil)
}

// SelectFromListWithAction creates an interactive selector with a custom action
// The customAction is triggered by r, and actionKey describes the action in the help text
func SelectFromListWithAction[T any](items []T, renderer ItemRenderer[T], customAction CustomAction[T], actionKey string, onOpen CustomAction[T], filterFunc func(T, bool) bool, onSelect CustomAction[T]) (T, error) {
	// Convert items to list items
	listItems := make([]list.Item, len(items))
	for i, item := range items {
		listItems[i] = listItem[T]{value: item, item: renderer}
	}

	l := list.New(listItems, itemDelegate[T]{renderer}, 100, 20)

	// Brighten the status bar when filtering so the "N filtered" text remains readable.
	styles := list.DefaultStyles()
	styles.StatusBarFilterCount = lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")). // Light gray for dark backgrounds
		Bold(true)
	styles.StatusBarActiveFilter = lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")). // Brighter for the active filter value
		Bold(true)
	styles.StatusBar = styles.StatusBar.Foreground(lipgloss.Color("247"))
	l.Styles = styles

	// Make the filter bar high-contrast so typed text is visible.
	l.FilterInput.Prompt = "Filter: "
	l.FilterInput.PromptStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")). // yellow-leaning white
		Bold(true)
	l.FilterInput.TextStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("231")) // white text while typing
	l.FilterInput.Cursor.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color("213")). // bright magenta cursor
		Bold(true)
	l.SetShowFilter(true)

	l.Title = "Select an item"
	l.SetShowStatusBar(true)
	l.SetShowPagination(true)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)

	m := &SelectionModel[T]{
		list:         l,
		items:        items,
		result:       make([]T, 0),
		customAction: customAction,
		actionKey:    actionKey,
		onOpen:       onOpen,
		viewport:     viewport.New(0, 0),
		filterFunc:   filterFunc,
		renderer:     renderer,
		onSelect:     onSelect,
	}

	if filterFunc != nil {
		m.filterActive = true
		m.updateVisibleItems()
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return *new(T), err
	}

	m = finalModel.(*SelectionModel[T])
	if len(m.result) == 0 {
		return *new(T), ErrNoSelection
	}

	return m.result[0], nil
}

// Init initializes the model
func (m *SelectionModel[T]) Init() tea.Cmd {
	return tea.Batch(tea.EnterAltScreen)
}

// Update handles user input
func (m *SelectionModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle detail view navigation
	if m.showDetail {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "esc", "q", "backspace":
				m.showDetail = false
				return m, nil
			}
		case tea.WindowSizeMsg:
			m.windowSize = msg
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	// When the help overlay is open, keep interaction scoped to the help view.
	if m.showHelp {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "?", "esc":
				m.showHelp = false
				return m, nil
			case "ctrl+c", "q":
				return m, tea.Quit
			}
		case tea.WindowSizeMsg:
			m.windowSize = msg
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// If we are filtering, let the list handle the input
		if m.list.FilterState() == list.Filtering {
			break
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "h":
			if m.filterFunc != nil {
				m.filterActive = !m.filterActive
				m.updateVisibleItems()
				return m, nil
			}
		case "o":
			if m.onOpen != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					msg, err := m.onOpen(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					if msg != "" {
						return m, m.list.NewStatusMessage(msg)
					}
				}
			}
			return m, nil
		case "down", "j":
			m.list.CursorDown()
			// Skip items that are marked as skippable (e.g. preview lines)
			for {
				selectedItem := m.list.SelectedItem()
				if selectedItem == nil {
					break
				}
				item := selectedItem.(listItem[T])
				if !m.renderer.IsSkippable(item.value) {
					break
				}
				// If we hit the bottom and it's skippable, we can't go further down.
				// We should probably go back up to the last non-skippable item.
				if m.list.Index() == len(m.list.Items())-1 {
					m.list.CursorUp()
					continue // Loop will check if new item is skippable (it shouldn't be if we came from there)
				}
				m.list.CursorDown()
			}
			return m, nil
		case "up", "k":
			m.list.CursorUp()
			// Skip items that are marked as skippable
			for {
				selectedItem := m.list.SelectedItem()
				if selectedItem == nil {
					break
				}
				item := selectedItem.(listItem[T])
				if !m.renderer.IsSkippable(item.value) {
					break
				}
				// If we hit the top and it's skippable
				if m.list.Index() == 0 {
					m.list.CursorDown()
					continue
				}
				m.list.CursorUp()
			}
			return m, nil
		case "enter":
			selected := m.list.SelectedItem()
			if selected != nil {
				item := selected.(listItem[T])

				if m.onSelect != nil {
					msg, err := m.onSelect(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}

					if msg == "SHOW_DETAIL" {
						// Show detail view
						m.showDetail = true

						previewStart := time.Now()
						content := item.item.Preview(item.value)
						fmt.Fprintf(os.Stderr, "[DEBUG] Preview() call took %v\n", time.Since(previewStart))

						wrapStart := time.Now()
						wrappedContent := WrapText(content, m.viewport.Width)
						fmt.Fprintf(os.Stderr, "[DEBUG] WrapText() took %v, content length: %d\n", time.Since(wrapStart), len(content))

						setContentStart := time.Now()
						m.viewport.SetContent(wrappedContent)
						fmt.Fprintf(os.Stderr, "[DEBUG] viewport.SetContent() took %v\n", time.Since(setContentStart))

						m.viewport.GotoTop()
						return m, nil
					}

					// Assume it was a toggle or action that requires refresh
					m.updateVisibleItems()
					// Force update of the item in the list to reflect changes
					m.list.SetItem(m.list.Index(), item)

					if msg != "" {
						return m, m.list.NewStatusMessage(msg)
					}
					return m, nil
				}

				if m.onOpen != nil {
					// Browse mode: Show Detail
					m.showDetail = true

					previewStart := time.Now()
					content := item.item.Preview(item.value)
					fmt.Fprintf(os.Stderr, "[DEBUG] Preview() call took %v\n", time.Since(previewStart))

					wrapStart := time.Now()
					wrappedContent := WrapText(content, m.viewport.Width)
					fmt.Fprintf(os.Stderr, "[DEBUG] WrapText() took %v, content length: %d\n", time.Since(wrapStart), len(content))

					setContentStart := time.Now()
					m.viewport.SetContent(wrappedContent)
					fmt.Fprintf(os.Stderr, "[DEBUG] viewport.SetContent() took %v\n", time.Since(setContentStart))

					// Reset viewport position
					m.viewport.GotoTop()
					return m, nil
				}
				// Apply mode: Select and Return
				m.result = append(m.result, item.value)
			}
			return m, tea.Quit
		case "ctrl+e":
			// Edit in $EDITOR
			selected := m.list.SelectedItem()
			if selected != nil {
				item := selected.(listItem[T])
				if editPath := item.item.EditPath(item.value); editPath != "" {
					_ = m.editInEditor(editPath, item.item.EditLine(item.value))
				}
			}
			return m, nil
		case "r":
			// Custom action (e.g., resolve)
			if m.customAction != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					msg, err := m.customAction(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					// Force update of the item in the list to reflect changes
					m.list.SetItem(m.list.Index(), item)

					if msg != "" {
						return m, m.list.NewStatusMessage(msg)
					}
				}
			}
			return m, nil
		case "right", "l":
			// Also allow right arrow or 'l' to enter detail view if in browse mode
			if m.onOpen != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					m.showDetail = true
					content := item.item.Preview(item.value)
					wrappedContent := WrapText(content, m.viewport.Width)
					m.viewport.SetContent(wrappedContent)
					m.viewport.GotoTop()
					return m, nil
				}
			}
		}
	case tea.WindowSizeMsg:
		m.windowSize = msg
		h, v := lipgloss.NewStyle().GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// editInEditor opens a file in $EDITOR at the specified line
func (m *SelectionModel[T]) editInEditor(filePath string, lineNum int) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	var cmd *exec.Cmd
	if lineNum > 0 {
		// Use +line convention
		cmd = exec.Command(editor, fmt.Sprintf("+%d", lineNum), filePath)
	} else {
		cmd = exec.Command(editor, filePath)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// updateVisibleItems updates the list items based on the current filter state
func (m *SelectionModel[T]) updateVisibleItems() {
	var visible []list.Item
	for _, item := range m.items {
		if m.filterFunc == nil || m.filterFunc(item, m.filterActive) {
			visible = append(visible, listItem[T]{value: item, item: m.renderer})
		}
	}
	m.list.SetItems(visible)
}

// View renders the model
func (m *SelectionModel[T]) View() string {
	if m.showDetail {
		return m.viewport.View()
	}

	if m.showHelp {
		return m.renderHelpOverlay()
	}

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")). // Brighter gray for better visibility
		Italic(true)

	var helpText string
	if !m.showHelp {
		// Compact view
		helpText = "↑/↓ navigate • enter details • o open • r resolve • h show/hide resolved • / search • q quit • ? help"
	} else {
		helpText = ""
	}

	help := helpStyle.Render(helpText)

	// Top section with title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("36")).
		Bold(true)
	title := titleStyle.Render("Select a comment")

	// Combine: title + list
	listSection := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		m.list.View(),
	)

	// Bottom: help text
	content := lipgloss.JoinVertical(
		lipgloss.Top,
		listSection,
		help,
	)

	return content
}

// renderHelpOverlay draws a centered help modal similar to the default Bubble Tea help panel.
func (m *SelectionModel[T]) renderHelpOverlay() string {
	width := m.windowSize.Width
	height := m.windowSize.Height

	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	type entry struct {
		key  string
		desc string
	}

	entries := []entry{
		{"↑/↓", "Navigate"},
		{"enter", "Select or open details"},
		{"q", "Quit"},
		{"?", "Close help"},
	}

	if m.onOpen != nil {
		entries = append(entries, entry{"o", "Open in browser"})
	}

	entries = append(entries, entry{"ctrl+e", "Edit in $EDITOR"})

	if m.filterFunc != nil {
		entries = append(entries, entry{"h", "Toggle show resolved"})
	}

	if m.customAction != nil && m.actionKey != "" {
		key, desc := splitActionKey(m.actionKey)
		entries = append(entries, entry{key, desc})
	}

	entries = append(entries, entry{"/", "Search"})

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("207")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	keyCell := lipgloss.NewStyle().Width(12).Align(lipgloss.Right)

	var rows []string
	for _, e := range entries {
		rows = append(rows, lipgloss.JoinHorizontal(
			lipgloss.Left,
			keyCell.Render(keyStyle.Render(e.key)),
			"  ",
			descStyle.Render(e.desc),
		))
	}

	title := lipgloss.NewStyle().Foreground(lipgloss.Color("36")).Bold(true).Render("Help")
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("Press ? to return")

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		subtitle,
		"",
		strings.Join(rows, "\n"),
	)

	boxWidth := width - 8
	if boxWidth > 72 {
		boxWidth = 72
	}
	if boxWidth < 32 {
		boxWidth = width - 2
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(1, 2).
		Width(boxWidth).
		Render(body)

	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		box,
	)
}

func splitActionKey(actionKey string) (string, string) {
	parts := strings.Fields(actionKey)
	if len(parts) == 0 {
		return actionKey, ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

// itemDelegate renders individual list items
type itemDelegate[T any] struct {
	renderer ItemRenderer[T]
}

func (d itemDelegate[T]) Height() int {
	return 1
}

func (d itemDelegate[T]) Spacing() int {
	return 0
}

func (d itemDelegate[T]) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return nil
}

func (d itemDelegate[T]) Render(w io.Writer, m list.Model, index int, item list.Item) {
	li := item.(listItem[T])
	title := d.renderer.Title(li.value)
	desc := d.renderer.Description(li.value)

	isSelected := index == m.Index()

	var s strings.Builder

	if isSelected {
		// Cursor
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render("▶ "))

		// Selected Item Style
		// We use a background color to make it prominent
		selectedBg := lipgloss.Color("57")  // Indigo/Purple
		selectedFg := lipgloss.Color("255") // White

		titleStyle := lipgloss.NewStyle().
			Foreground(selectedFg).
			Background(selectedBg).
			Bold(true)

		s.WriteString(titleStyle.Render(title))

		if desc != "" {
			descStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("252")). // Slightly dimmer than white
				Background(selectedBg)
			s.WriteString(descStyle.Render(" " + desc))
		}
	} else {
		// Unselected
		s.WriteString("  ")

		titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
		s.WriteString(titleStyle.Render(title))

		if desc != "" {
			s.WriteString(" ")
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
			s.WriteString(descStyle.Render(desc))
		}
	}

	_, _ = fmt.Fprint(w, s.String())
}
