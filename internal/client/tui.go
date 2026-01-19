package client

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sonnytaylor/exio/pkg/protocol"
)

// TUI styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Padding(0, 1)

	urlStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	methodStyle = lipgloss.NewStyle().
			Bold(true).
			Width(7)

	pathStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	durationStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Width(8).
			Align(lipgloss.Right)

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Width(10)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 1)
)

// Status code colors
func statusStyle(code int) lipgloss.Style {
	style := lipgloss.NewStyle().Width(4)
	switch {
	case code >= 200 && code < 300:
		return style.Foreground(lipgloss.Color("42")) // Green
	case code >= 300 && code < 400:
		return style.Foreground(lipgloss.Color("214")) // Orange
	case code >= 400 && code < 500:
		return style.Foreground(lipgloss.Color("220")) // Yellow
	case code >= 500:
		return style.Foreground(lipgloss.Color("196")) // Red
	default:
		return style.Foreground(lipgloss.Color("252"))
	}
}

// Method colors
func methodColor(method string) lipgloss.Style {
	style := methodStyle
	switch method {
	case "GET":
		return style.Foreground(lipgloss.Color("42"))
	case "POST":
		return style.Foreground(lipgloss.Color("214"))
	case "PUT":
		return style.Foreground(lipgloss.Color("220"))
	case "DELETE":
		return style.Foreground(lipgloss.Color("196"))
	case "PATCH":
		return style.Foreground(lipgloss.Color("135"))
	default:
		return style.Foreground(lipgloss.Color("252"))
	}
}

// TUIModel is the Bubbletea model for the Exio TUI.
type TUIModel struct {
	client       *Client
	requests     []protocol.RequestLog
	viewport     viewport.Model
	ready        bool
	width        int
	height       int
	quitting     bool
	maxRequests  int
}

// NewTUIModel creates a new TUI model for the given client.
func NewTUIModel(client *Client) TUIModel {
	return TUIModel{
		client:      client,
		requests:    make([]protocol.RequestLog, 0),
		maxRequests: 100,
	}
}

// requestMsg is sent when a new request is logged.
type requestMsg protocol.RequestLog

// tickMsg is sent periodically to update stats.
type tickMsg time.Time

// Init initializes the TUI model.
func (m TUIModel) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles TUI events.
func (m TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "c":
			// Clear requests
			m.requests = make([]protocol.RequestLog, 0)
			m.updateViewport()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 6
		footerHeight := 2
		verticalMargins := headerHeight + footerHeight

		if !m.ready {
			m.viewport = viewport.New(msg.Width-4, msg.Height-verticalMargins)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = msg.Width - 4
			m.viewport.Height = msg.Height - verticalMargins
		}
		m.updateViewport()

	case requestMsg:
		m.requests = append(m.requests, protocol.RequestLog(msg))
		if len(m.requests) > m.maxRequests {
			m.requests = m.requests[1:]
		}
		m.updateViewport()
		// Auto-scroll to bottom
		m.viewport.GotoBottom()

	case tickMsg:
		cmds = append(cmds, tickCmd())
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// updateViewport updates the viewport content with the request log.
func (m *TUIModel) updateViewport() {
	if !m.ready {
		return
	}

	var content strings.Builder

	if len(m.requests) == 0 {
		content.WriteString("\n  Waiting for requests...\n")
	} else {
		for _, req := range m.requests {
			line := fmt.Sprintf("%s  %s  %s  %s  %s\n",
				timestampStyle.Render(req.Timestamp.Format("15:04:05")),
				methodColor(req.Method).Render(req.Method),
				statusStyle(req.StatusCode).Render(fmt.Sprintf("%d", req.StatusCode)),
				durationStyle.Render(formatDuration(req.Duration)),
				pathStyle.Render(truncatePath(req.Path, m.width-40)),
			)
			content.WriteString(line)
		}
	}

	m.viewport.SetContent(content.String())
}

// View renders the TUI.
func (m TUIModel) View() string {
	if m.quitting {
		return ""
	}

	if !m.ready {
		return "Initializing..."
	}

	// Header
	title := titleStyle.Render("Exio Tunnel Active")
	url := urlStyle.Render(m.client.PublicURL())
	
	requestCount, _, _, connectedAt := m.client.Stats()
	uptime := time.Since(connectedAt).Round(time.Second)
	stats := statusBarStyle.Render(fmt.Sprintf(
		"Requests: %d | Uptime: %s",
		requestCount, uptime,
	))

	header := fmt.Sprintf("%s\n%s\n%s\n", title, url, stats)

	// Footer
	help := helpStyle.Render("q: quit | c: clear | scroll: up/down")

	// Combine
	return fmt.Sprintf("%s\n%s\n%s", header, m.viewport.View(), help)
}

// AddRequest adds a request to the TUI log.
func (m *TUIModel) AddRequest(log protocol.RequestLog) tea.Cmd {
	return func() tea.Msg {
		return requestMsg(log)
	}
}

// formatDuration formats a duration for display.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// truncatePath truncates a path to fit the display width.
func truncatePath(path string, maxWidth int) string {
	if maxWidth < 10 {
		maxWidth = 10
	}
	if len(path) <= maxWidth {
		return path
	}
	return path[:maxWidth-3] + "..."
}

// RunTUI starts the TUI for the client.
func RunTUI(client *Client) error {
	model := NewTUIModel(client)

	// Set up the client callback to send requests to the TUI
	p := tea.NewProgram(model, tea.WithAltScreen())

	client.OnRequest = func(log protocol.RequestLog) {
		p.Send(requestMsg(log))
	}

	_, err := p.Run()
	return err
}
