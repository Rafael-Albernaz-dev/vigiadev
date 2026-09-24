package tui

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

type serviceRowZone struct {
	name   string
	tabIdx int
	startY int
	endY   int
}

type probeStatus struct {
	Type    domain.HealthCheckType
	Target  string
	Latency time.Duration
	Success bool
	Error   string
}

type SystemDoctorInfo struct {
	OS        string
	Docker    string
	DockerOK  bool
	Compose   string
	ComposeOK bool
	Git       string
	GitOK     bool
}

func checkSystemPrereqs() SystemDoctorInfo {
	info := SystemDoctorInfo{
		OS: fmt.Sprintf("%s / %s (%d CPUs)", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
	}

	// 1. Docker Engine
	conn, err := net.DialTimeout("unix", "/var/run/docker.sock", 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		info.Docker = "Active (/var/run/docker.sock)"
		info.DockerOK = true
	} else {
		info.Docker = "Unavailable"
		info.DockerOK = false
	}

	// 2. Docker Compose
	cmdCompose := exec.Command("docker", "compose", "version", "--short")
	if out, err := cmdCompose.Output(); err == nil && len(out) > 0 {
		info.Compose = strings.TrimSpace(string(out))
		info.ComposeOK = true
	} else {
		cmdCompose = exec.Command("docker", "compose", "version")
		if out, err := cmdCompose.Output(); err == nil {
			info.Compose = strings.TrimSpace(string(out))
			info.ComposeOK = true
		} else {
			info.Compose = "Not responsive"
			info.ComposeOK = false
		}
	}

	// 3. Git
	cmdGit := exec.Command("git", "version")
	if out, err := cmdGit.Output(); err == nil {
		info.Git = strings.TrimSpace(strings.TrimPrefix(string(out), "git version "))
		info.GitOK = true
	} else {
		info.Git = "Not found"
		info.GitOK = false
	}

	return info
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
	ProjectName     string
	services        []string
	cards           map[string]*ServiceCardState
	tabs            []string
	activeTab       int
	tabRowYStart    int
	tabRowYEnd      int
	tabZones        []tabZone
	serviceZones    []serviceRowZone
	logs            map[string][]string
	viewport        viewport.Model
	width           int
	height          int
	ready           bool
	bus             *domain.EventBus
	cancel          context.CancelFunc
	restartFunc     func(service string) error
	statusMsg       string
	isFiltering     bool
	filterQuery     string
	diagnosticsOpen bool
	doctorInfo      SystemDoctorInfo
	probes          map[string]probeStatus
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
		probes:      make(map[string]probeStatus),
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
		if m.diagnosticsOpen {
			switch msg.String() {
			case "d", "esc":
				m.diagnosticsOpen = false
			}
			return m, nil
		}
		if m.isFiltering {
			switch msg.String() {
			case "esc":
				m.isFiltering = false
				m.filterQuery = ""
				m.syncViewport()
				return m, nil
			case "enter":
				m.isFiltering = false
				m.syncViewport()
				return m, nil
			case "backspace":
				if len(m.filterQuery) > 0 {
					m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
					m.syncViewport()
				}
				return m, nil
			case "ctrl+c":
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			default:
				if len(msg.Runes) > 0 {
					m.filterQuery += string(msg.Runes)
					m.syncViewport()
					return m, nil
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "d":
			m.diagnosticsOpen = true
			m.doctorInfo = checkSystemPrereqs()
			return m, nil

		case "/":
			currentTab := m.tabs[m.activeTab]
			if currentTab != "METRICS" {
				m.isFiltering = true
				return m, nil
			}

		case "esc":
			if m.filterQuery != "" {
				m.filterQuery = ""
				m.syncViewport()
				return m, nil
			}

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

		// 2. Mouse click handling (tab bar or status card row)
		isLeftClick := msg.Button == tea.MouseButtonLeft || msg.Type == tea.MouseLeft
		if isLeftClick {
			// A. Tab Bar click (with ±1 line tolerance)
			if m.tabRowYStart > 0 && msg.Y >= m.tabRowYStart-1 && msg.Y <= m.tabRowYEnd+1 {
				for _, tz := range m.tabZones {
					if msg.X >= tz.startX && msg.X <= tz.endX {
						m.activeTab = tz.index
						m.syncViewport()
						return m, nil
					}
				}
			}

			// B. Status card service row click (switches directly to service tab)
			for _, sz := range m.serviceZones {
				if msg.Y >= sz.startY && msg.Y <= sz.endY {
					m.activeTab = sz.tabIdx
					m.syncViewport()
					return m, nil
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Header is: title (2) + statusBox (len(services) + 3) + tabs (2)
		headerHeight := 4 + len(m.services) + 3
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
		timestamp := msg.OccurredAt()
		if timestamp.IsZero() {
			timestamp = time.Now()
		}
		timeStr := timestamp.Format("15:04:05")
		timeStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("#767676")).Render(timeStr)
		serviceStyled := lipgloss.NewStyle().Bold(true).Foreground(accentColor).Render(fmt.Sprintf("[%s]", msg.Service))

		content := msg.Line
		if msg.IsError {
			content = lipgloss.NewStyle().Foreground(errorColor).Render(content)
		}

		allLine := fmt.Sprintf("%s %s %s", timeStyled, serviceStyled, content)
		svcLine := fmt.Sprintf("%s %s", timeStyled, content)

		m.logs["ALL"] = append(m.logs["ALL"], allLine)
		if _, exists := m.logs[msg.Service]; exists {
			m.logs[msg.Service] = append(m.logs[msg.Service], svcLine)
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
	case domain.HealthCheckProbed:
		m.probes[msg.Service] = probeStatus{Type: msg.Type, Target: msg.Target, Latency: msg.Latency, Success: msg.Success, Error: msg.Error}
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
	if m.filterQuery != "" {
		query := strings.ToLower(m.filterQuery)
		filtered := make([]string, 0, len(lines))
		for _, l := range lines {
			if strings.Contains(strings.ToLower(ansi.Strip(l)), query) {
				filtered = append(filtered, l)
			}
		}
		lines = filtered
	}
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
	if m.diagnosticsOpen {
		return m.renderDiagnostics()
	}
	if !m.ready {
		return "\n  Initializing vigiaDev TUI..."
	}

	var b strings.Builder

	// 1. Header
	header := titleStyle.Render(fmt.Sprintf(" vigiaDev • %s ", m.ProjectName))
	b.WriteString(header + "\n\n")

	// 2. Consolidated Services Status Card
	var statusCardContent strings.Builder

	// Box Header Title
	cardTitle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(highlightColor).
		Padding(0, 1).
		Render(" SERVICES HEALTH & STATUS ")
	statusCardContent.WriteString(cardTitle + "\n")

	// Determine max service name width for neat column alignment
	maxNameLen := 14
	for _, name := range m.services {
		if len(name) > maxNameLen {
			maxNameLen = len(name)
		}
	}
	if maxNameLen > 24 {
		maxNameLen = 24
	}

	boxStartY := lipgloss.Height(b.String())
	m.serviceZones = make([]serviceRowZone, 0, len(m.services))

	for i, name := range m.services {
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

		stateStr := strings.ToUpper(string(card.State))
		stateBadge := lipgloss.NewStyle().Foreground(stateColor).Bold(true).Width(12).Render(fmt.Sprintf("%s %s", icon, stateStr))

		namePadded := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).Width(maxNameLen + 2).Render(truncate(card.Name, maxNameLen))

		portStr := "-"
		if card.Port > 0 {
			if card.IsRemapped {
				portStr = fmt.Sprintf(":%d➔:%d", card.OriginalPort, card.Port)
			} else {
				portStr = fmt.Sprintf(":%d", card.Port)
			}
		}
		portCol := lipgloss.NewStyle().Foreground(accentColor).Width(14).Render(portStr)

		resStr := "-"
		if card.CPUPercent > 0 || card.MemoryBytes > 0 {
			resStr = fmt.Sprintf("%s CPU • %s", telemetry.FormatCPU(card.CPUPercent), telemetry.FormatBytes(card.MemoryBytes))
		}
		resCol := lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0")).Width(22).Render(resStr)

		detailStr := ""
		if card.Detail != "" {
			detailStr = card.Detail
		}
		detailCol := lipgloss.NewStyle().Foreground(subtleColor).Render(truncate(detailStr, 36))

		row := fmt.Sprintf("%s  %s  %s  %s  %s", stateBadge, namePadded, portCol, resCol, detailCol)
		statusCardContent.WriteString(row)
		if i < len(m.services)-1 {
			statusCardContent.WriteString("\n")
		}

		// Track clickable row zone (tabs: 0=ALL, 1..N=services, N+1=METRICS)
		tabIdx := i + 1
		rowY := boxStartY + 1 + 1 + i // box border (1) + cardTitle (1) + index (i)
		m.serviceZones = append(m.serviceZones, serviceRowZone{
			name:   name,
			tabIdx: tabIdx,
			startY: rowY,
			endY:   rowY,
		})
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(0, 1).
		MarginBottom(1)

	if m.width > 20 {
		boxStyle = boxStyle.Width(m.width - 2)
	}

	renderedBox := boxStyle.Render(statusCardContent.String())
	b.WriteString(renderedBox + "\n")

	// 3. Tab Bar (with exact click coordinate mapping)
	tabRowStart := lipgloss.Height(b.String())
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
		curX += tabW

		tabsRow.WriteString(renderedTab)
	}

	tabsContent := tabsRow.String()
	tabsHeight := lipgloss.Height(tabsContent)
	m.tabRowYStart = tabRowStart
	m.tabRowYEnd = tabRowStart + tabsHeight - 1

	b.WriteString(tabsContent + "\n")

	// 4. Viewport (Logs or Metrics)
	b.WriteString(m.viewport.View() + "\n")

	// 5. Interactive Footer
	currentTab := m.tabs[m.activeTab]
	var footerText string

	if m.isFiltering {
		filterPrompt := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(highlightColor).
			Padding(0, 1).
			Render(fmt.Sprintf("FILTER: /%s█", m.filterQuery))
		filterHelp := lipgloss.NewStyle().Foreground(subtleColor).Render("  [Enter] Apply  •  [Esc] Clear & Exit")
		b.WriteString(filterPrompt + filterHelp)
		return b.String()
	}

	if currentTab != "ALL" && currentTab != "METRICS" {
		footerText = fmt.Sprintf("[Tab/Click] Switch  •  [/] Filter  •  [r] RESTART '%s'  •  [c] Clear  •  [q] Quit", currentTab)
	} else if currentTab == "METRICS" {
		footerText = "[Tab/Click] Switch  •  [Scroll/Arrows] Scroll Metrics  •  [q] Quit"
	} else {
		footerText = "[Tab/Click] Switch  •  [/] Filter  •  [1-9] Quick Jump  •  [Scroll/Arrows] Scroll  •  [q] Quit"
	}

	if m.filterQuery != "" && currentTab != "METRICS" {
		matchesCount := 0
		query := strings.ToLower(m.filterQuery)
		for _, l := range m.logs[currentTab] {
			if strings.Contains(strings.ToLower(ansi.Strip(l)), query) {
				matchesCount++
			}
		}
		badge := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(warningColor).
			Padding(0, 1).
			Render(fmt.Sprintf("FILTER: \"%s\" (%d lines)", m.filterQuery, matchesCount))
		footerText = fmt.Sprintf("%s [Esc: Clear]  •  %s", badge, footerText)
	}

	if m.statusMsg != "" {
		footerText = fmt.Sprintf("[vigiadev] %s  •  %s", m.statusMsg, footerText)
	}

	b.WriteString(footerStyle.Render(footerText))

	return b.String()
}

func (m *AppModel) renderDiagnostics() string {
	modalWidth := m.width - 6
	if modalWidth > 96 {
		modalWidth = 96
	}
	if modalWidth < 68 {
		modalWidth = 68
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FAFAFA")).
		Background(highlightColor).
		Padding(0, 1)

	sectionTitleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(accentColor)

	subtleTextStyle := lipgloss.NewStyle().
		Foreground(subtleColor)

	// Modal Header
	b.WriteString(headerStyle.Render("🩺 ENVIRONMENT & HEALTH DIAGNOSTICS (Doctor)"))
	b.WriteString("\n\n")

	// Section 1: SYSTEM PREREQUISITES
	b.WriteString(sectionTitleStyle.Render("SYSTEM & ENVIRONMENT PREREQUISITES"))
	b.WriteByte('\n')

	dockerIcon := "✅"
	if !m.doctorInfo.DockerOK {
		dockerIcon = "⚠️"
	}
	composeIcon := "✅"
	if !m.doctorInfo.ComposeOK {
		composeIcon = "⚠️"
	}
	gitIcon := "✅"
	if !m.doctorInfo.GitOK {
		gitIcon = "⚠️"
	}

	osDisplay := truncate(m.doctorInfo.OS, 28)
	dockerDisplay := truncate(dockerIcon+" "+m.doctorInfo.Docker, 34)
	gitDisplay := truncate(gitIcon+" "+m.doctorInfo.Git, 28)
	composeDisplay := truncate(composeIcon+" "+m.doctorInfo.Compose, 34)

	fmt.Fprintf(&b, "  • OS / Arch:   %-28s  Docker Engine:  %s\n", osDisplay, dockerDisplay)
	fmt.Fprintf(&b, "  • Git Version: %-28s  Docker Compose: %s\n\n", gitDisplay, composeDisplay)

	// Section 2: SERVICES HEALTHCHECKS & READINESS PROBES
	b.WriteString(sectionTitleStyle.Render("READINESS PROBES & SERVICE HEALTH"))
	b.WriteByte('\n')

	colHeader := fmt.Sprintf("  %-9s %-16s %-6s %-18s %-9s %s", "STATUS", "SERVICE", "TYPE", "TARGET", "LATENCY", "DETAILS")
	b.WriteString(subtleTextStyle.Render(colHeader))
	b.WriteByte('\n')
	separatorLen := modalWidth - 8
	if separatorLen < 20 {
		separatorLen = 20
	}
	b.WriteString(subtleTextStyle.Render("  " + strings.Repeat("─", separatorLen)))
	b.WriteByte('\n')

	for _, name := range m.services {
		p, hasProbe := m.probes[name]
		card := m.cards[name]

		statusText := "⏳ Pending"
		probeType := "TCP"
		target := "-"
		latencyStr := "-"
		detail := "Waiting for probe..."

		if card != nil && card.Port > 0 {
			target = fmt.Sprintf("127.0.0.1:%d", card.Port)
			if card.IsRemapped {
				target = fmt.Sprintf(":%d [REMAP]", card.Port)
			}
		}

		if hasProbe {
			if p.Type != "" {
				probeType = strings.ToUpper(string(p.Type))
			}
			if p.Target != "" {
				target = p.Target
			}
			if p.Success {
				statusText = "✅ Ready"
				latencyStr = fmt.Sprintf("%dms", p.Latency.Milliseconds())
				if latencyStr == "0ms" && p.Latency > 0 {
					latencyStr = fmt.Sprintf("%.1fms", float64(p.Latency.Microseconds())/1000.0)
				}
				detail = "Healthy & responsive"
			} else {
				statusText = "❌ Failed"
				if p.Latency > 0 {
					latencyStr = fmt.Sprintf("%dms", p.Latency.Milliseconds())
				}
				if p.Error != "" {
					detail = p.Error
				} else {
					detail = "Connection failed"
				}
			}
		} else if card != nil {
			if card.State == domain.StateHealthy {
				statusText = "✅ Ready"
				detail = "Active (healthy)"
			} else if card.State == domain.StateStarting {
				statusText = "⏳ Starting"
				detail = "Service starting..."
			} else if card.State == domain.StateFailed {
				statusText = "❌ Failed"
				if card.Detail != "" {
					detail = card.Detail
				} else {
					detail = "Process crashed / exited"
				}
			} else if card.Detail != "" {
				detail = card.Detail
			}
		}

		maxDetailWidth := modalWidth - 66
		if maxDetailWidth < 12 {
			maxDetailWidth = 12
		}

		targetFormatted := truncate(target, 18)
		detailFormatted := truncate(detail, maxDetailWidth)

		fmt.Fprintf(&b, "  %-9s %-16s %-6s %-18s %-9s %s\n",
			statusText,
			truncate(name, 16),
			probeType,
			targetFormatted,
			latencyStr,
			detailFormatted,
		)
	}

	b.WriteString("\n")
	footerHint := subtleTextStyle.Render("Press [d] or [Esc] to dismiss  •  Press [r] to restart focused service")
	b.WriteString(footerHint)

	panel := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(highlightColor).
		Padding(1, 2).
		Width(modalWidth).
		Render(b.String())

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
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
