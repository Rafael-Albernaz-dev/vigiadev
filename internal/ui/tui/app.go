package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/telemetry"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// Lip Gloss colors and styling
var (
	subtleColor    = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	highlightColor = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	accentColor    = lipgloss.AdaptiveColor{Light: "#00ADD8", Dark: "#00ADD8"}
	successColor   = lipgloss.AdaptiveColor{Light: "#02BF87", Dark: "#02BF87"}
	warningColor   = lipgloss.AdaptiveColor{Light: "#FFB300", Dark: "#FFB300"}
	errorColor     = lipgloss.AdaptiveColor{Light: "#FF4D4D", Dark: "#FF4D4D"}

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(highlightColor).
			Padding(0, 1)

	cardBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtleColor).
			Padding(0, 1).
			MarginRight(1)

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(highlightColor).
			Padding(0, 1).
			MarginRight(1)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A0A0A0")).
				Background(lipgloss.Color("#2C2C2C")).
				Padding(0, 1).
				MarginRight(1)

	footerStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			Padding(0, 1)
)

type tabZone struct {
	index  int
	startX int
	endX   int
}

// ServiceCardState stores visual state and telemetry data for each supervised service.
type ServiceCardState struct {
	Name           string
	State          domain.ServiceState
	Port           int
	OriginalPort   int
	IsRemapped     bool
	Detail         string
	CPUPercent     float64
	MemoryBytes    uint64
	DiskReadBytes  uint64
	DiskWriteBytes uint64
	ResponseTime   time.Duration
}

// AppModel is the primary Bubble Tea model for vigiaDev.
type AppModel struct {
	ProjectName string
	services    []string
	cards       map[string]*ServiceCardState
	tabs        []string
	activeTab   int
	tabRowY     int
	tabZones    []tabZone
	logs        map[string][]string
	viewport    viewport.Model
	width       int
	height      int
	ready       bool
	bus         *domain.EventBus
	cancel      context.CancelFunc
	restartFunc func(service string) error
	statusMsg   string
}

// NewAppModel instantiates the interactive TUI model.
func NewAppModel(projectName string, serviceNames []string, bus *domain.EventBus, cancel context.CancelFunc, restartFunc func(service string) error) *AppModel {
	tabs := append([]string{"ALL"}, serviceNames...)
	tabs = append(tabs, "METRICS")

	cards := make(map[string]*ServiceCardState)
	logs := make(map[string][]string)

	logs["ALL"] = make([]string, 0)
	for _, name := range serviceNames {
		cards[name] = &ServiceCardState{
			Name:  name,
			State: domain.StatePending,
		}
		logs[name] = make([]string, 0)
	}

	return &AppModel{
		ProjectName: projectName,
		services:    serviceNames,
		cards:       cards,
		tabs:        tabs,
		activeTab:   0,
		logs:        logs,
		bus:         bus,
		cancel:      cancel,
		restartFunc: restartFunc,
	}
}

func (m *AppModel) Init() tea.Cmd {
	return nil
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit

		case "tab":
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			m.syncViewport()

		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
			m.syncViewport()

		case "r":
			currentTab := m.tabs[m.activeTab]
			if currentTab != "ALL" && currentTab != "METRICS" && m.restartFunc != nil {
				m.statusMsg = fmt.Sprintf("Restarting service '%s'...", currentTab)
				go func(svc string) {
					_ = m.restartFunc(svc)
				}(currentTab)
			}

		case "c":
			currentTab := m.tabs[m.activeTab]
			if currentTab != "METRICS" {
				m.logs[currentTab] = make([]string, 0)
				m.syncViewport()
			}

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.tabs) {
				m.activeTab = idx
				m.syncViewport()
			}
		}

	case tea.MouseMsg:
		// 1. Mouse Wheel scrolling
		if msg.Button == tea.MouseButtonWheelUp || msg.Type == tea.MouseWheelUp {
			m.viewport.LineUp(3)
			return m, nil
		}
		if msg.Button == tea.MouseButtonWheelDown || msg.Type == tea.MouseWheelDown {
			m.viewport.LineDown(3)
			return m, nil
		}

		// 2. Mouse click on tab bar
		isClick := msg.Action == tea.MouseActionPress || msg.Action == tea.MouseActionRelease || msg.Type == tea.MouseLeft
		if isClick && (msg.Button == tea.MouseButtonLeft || msg.Type == tea.MouseLeft) {
			if m.tabRowY > 0 && msg.Y == m.tabRowY {
				for _, tz := range m.tabZones {
					if msg.X >= tz.startX && msg.X <= tz.endX {
						m.activeTab = tz.index
						m.syncViewport()
						break
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 8
		footerHeight := 2
		viewportHeight := msg.Height - headerHeight - footerHeight
		if viewportHeight < 5 {
			viewportHeight = 5
		}

		if !m.ready {
			m.viewport = viewport.New(msg.Width, viewportHeight)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = viewportHeight
		}

		m.syncViewport()

	case domain.LogLineProduced:
		line := fmt.Sprintf("[%s] %s", msg.Service, msg.Line)
		if msg.IsError {
			line = lipgloss.NewStyle().Foreground(errorColor).Render(line)
		}

		m.logs["ALL"] = append(m.logs["ALL"], line)
		if _, exists := m.logs[msg.Service]; exists {
			m.logs[msg.Service] = append(m.logs[msg.Service], line)
		}

		currentTab := m.tabs[m.activeTab]
		if currentTab == "ALL" || currentTab == msg.Service {
			m.syncViewport()
			m.viewport.GotoBottom()
		}

	case domain.ServiceStateChanged:
		if card, ok := m.cards[msg.Service]; ok {
			card.State = msg.NewState
			card.Detail = msg.Detail
		}
		if m.tabs[m.activeTab] == "METRICS" {
			m.syncViewport()
		}

	case domain.PortRemapped:
		if card, ok := m.cards[msg.Service]; ok {
			card.OriginalPort = msg.OriginalPort
			card.Port = msg.TargetPort
			card.IsRemapped = true
		}
		if m.tabs[m.activeTab] == "METRICS" {
			m.syncViewport()
		}

	case domain.TelemetryUpdated:
		if card, ok := m.cards[msg.Service]; ok {
			card.CPUPercent = msg.CPUPercent
			card.MemoryBytes = msg.MemoryBytes
			card.DiskReadBytes = msg.DiskReadBytes
			card.DiskWriteBytes = msg.DiskWriteBytes
			if msg.ResponseTime > 0 {
				card.ResponseTime = msg.ResponseTime
			}
		}
		if m.tabs[m.activeTab] == "METRICS" {
			m.syncViewport()
		}
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *AppModel) syncViewport() {
	currentTab := m.tabs[m.activeTab]
	if currentTab == "METRICS" {
		m.viewport.SetContent(m.renderMetricsDashboard())
		return
	}
	lines := m.logs[currentTab]
	content := strings.Join(lines, "\n")
	m.viewport.SetContent(content)
}

func (m *AppModel) renderMetricsDashboard() string {
	var b strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(accentColor).
		Padding(0, 1)

	b.WriteString(headerStyle.Render(" SYSTEM TELEMETRY & RESOURCE DASHBOARD ") + "\n\n")

	metricBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 2).
		MarginBottom(1)

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(accentColor).Width(24)
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA"))

	for _, name := range m.services {
		card := m.cards[name]
		var content strings.Builder

		// Service Title
		title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(highlightColor).Padding(0, 1).Render(fmt.Sprintf(" SERVICE: %s ", card.Name))
		content.WriteString(title + "\n\n")

		// Operational Status
		icon := "⚪"
		switch card.State {
		case domain.StateHealthy:
			icon = "🟢"
		case domain.StateStarting:
			icon = "🟡"
		case domain.StateFailed:
			icon = "🔴"
		case domain.StateStopped:
			icon = "⏹️"
		}
		statusDesc := strings.ToUpper(string(card.State))
		if card.Detail != "" {
			statusDesc = fmt.Sprintf("%s (%s)", statusDesc, card.Detail)
		}
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Operational State:"), fmt.Sprintf("%s %s", icon, statusDesc)))

		// Network Port
		portStr := "none declared"
		if card.Port > 0 {
			if card.IsRemapped {
				portStr = fmt.Sprintf("%d ➔ %d [DYNAMIC REMAP]", card.OriginalPort, card.Port)
			} else {
				portStr = fmt.Sprintf("%d (listening)", card.Port)
			}
		}
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Network Port (TCP):"), valStyle.Render(portStr)))

		// CPU Usage
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("CPU Usage:"), valStyle.Render(telemetry.FormatCPU(card.CPUPercent))))

		// RAM Usage
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Memory (RAM):"), valStyle.Render(fmt.Sprintf("%s (Resident Set / cgroup)", telemetry.FormatBytes(card.MemoryBytes)))))

		// Disk I/O
		diskStr := fmt.Sprintf("Read: %s  •  Write: %s", telemetry.FormatBytes(card.DiskReadBytes), telemetry.FormatBytes(card.DiskWriteBytes))
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Disk I/O:"), valStyle.Render(diskStr)))

		// Probe Latency / Response
		probeStr := "awaiting probe sample..."
		if card.Detail != "" && strings.Contains(card.Detail, "probe:") {
			probeStr = card.Detail
		}
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Probe Response:"), valStyle.Render(probeStr)))

		b.WriteString(metricBox.Render(content.String()) + "\n")
	}

	return b.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func (m *AppModel) View() string {
	if !m.ready {
		return "\n  Initializing vigiaDev TUI..."
	}

	var b strings.Builder

	// 1. Header
	header := titleStyle.Render(fmt.Sprintf(" vigiaDev • %s ", m.ProjectName))
	b.WriteString(header + "\n\n")

	// 2. Service Cards (Status + Port + Detail)
	var cardsRow strings.Builder
	for _, name := range m.services {
		card := m.cards[name]
		icon := "⚪"
		stateColor := subtleColor

		switch card.State {
		case domain.StateHealthy:
			icon = "🟢"
			stateColor = successColor
		case domain.StateStarting:
			icon = "🟡"
			stateColor = warningColor
		case domain.StateFailed:
			icon = "🔴"
			stateColor = errorColor
		case domain.StateStopped:
			icon = "⏹️"
		}

		portInfo := ""
		if card.Port > 0 {
			if card.IsRemapped {
				portInfo = fmt.Sprintf(" | :%d ➔ :%d [REMAP]", card.OriginalPort, card.Port)
			} else {
				portInfo = fmt.Sprintf(" | :%d", card.Port)
			}
		}

		stateStr := strings.ToUpper(string(card.State))
		detailLine := ""
		if card.Detail != "" {
			detailLine = "\n" + lipgloss.NewStyle().Foreground(subtleColor).Render(truncate(card.Detail, 32))
		}

		cardContent := fmt.Sprintf("%s %s%s\n%s%s",
			icon,
			lipgloss.NewStyle().Bold(true).Render(card.Name),
			portInfo,
			lipgloss.NewStyle().Foreground(stateColor).Bold(true).Render(stateStr),
			detailLine,
		)

		cardsRow.WriteString(cardBorder.Render(cardContent))
	}
	b.WriteString(cardsRow.String() + "\n\n")

	// 3. Tab Bar (with dynamic click coordinate mapping)
	m.tabRowY = lipgloss.Height(b.String()) - 1
	m.tabZones = make([]tabZone, len(m.tabs))
	var tabsRow strings.Builder
	curX := 0

	for i, tab := range m.tabs {
		tabLabel := fmt.Sprintf("%d:%s", i+1, tab)
		if tab != "METRICS" {
			count := len(m.logs[tab])
			tabLabel = fmt.Sprintf("%d:%s (%d)", i+1, tab, count)
		}

		var renderedTab string
		if i == m.activeTab {
			renderedTab = activeTabStyle.Render(tabLabel)
		} else {
			renderedTab = inactiveTabStyle.Render(tabLabel)
		}

		tabW := lipgloss.Width(renderedTab)
		m.tabZones[i] = tabZone{
			index:  i,
			startX: curX,
			endX:   curX + tabW,
		}
		curX += tabW + 1

		tabsRow.WriteString(renderedTab)
	}
	b.WriteString(tabsRow.String() + "\n")

	// 4. Viewport (Logs or Metrics)
	b.WriteString(m.viewport.View() + "\n")

	// 5. Interactive Footer
	currentTab := m.tabs[m.activeTab]
	var footerText string
	if currentTab != "ALL" && currentTab != "METRICS" {
		footerText = fmt.Sprintf("[Tab/Click] Switch Tab  •  [r] RESTART '%s'  •  [c] Clear Logs  •  [q] Quit", currentTab)
	} else if currentTab == "METRICS" {
		footerText = "[Tab/Click] Switch Tab  •  [Scroll/Arrows] Scroll Metrics  •  [q] Quit"
	} else {
		footerText = "[Tab/Click] Switch Tab  •  [1-9] Quick Jump  •  [Scroll/Arrows] Scroll Logs  •  [q] Quit"
	}

	if m.statusMsg != "" {
		footerText = fmt.Sprintf("[vigiadev] %s  •  %s", m.statusMsg, footerText)
	}

	b.WriteString(footerStyle.Render(footerText))

	return b.String()
}

// RunTUI starts the Bubble Tea program with mouse tracking and event bus bridge.
func RunTUI(ctx context.Context, projectName string, serviceNames []string, bus *domain.EventBus, cancel context.CancelFunc, restartFunc func(service string) error) error {
	model := NewAppModel(projectName, serviceNames, bus, cancel, restartFunc)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	bus.Subscribe(func(event domain.Event) {
		p.Send(event)
	})

	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, err := p.Run()
	return err
}
