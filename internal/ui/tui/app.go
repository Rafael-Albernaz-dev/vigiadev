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

// Cores e estilos Lip Gloss
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

// ServiceCardState armazena os metadados visuais de cada serviço no card superior e telemetria profunda.
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

// AppModel é o modelo principal do Bubble Tea.
type AppModel struct {
	ProjectName string
	services    []string
	cards       map[string]*ServiceCardState
	tabs        []string
	activeTab   int
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

// NewAppModel instancia o modelo da TUI interativa com suporte a aba de métricas e restart.
func NewAppModel(projectName string, serviceNames []string, bus *domain.EventBus, cancel context.CancelFunc, restartFunc func(service string) error) *AppModel {
	tabs := append([]string{"ALL"}, serviceNames...)
	tabs = append(tabs, "📊 MÉTRICAS")

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
			// Reinicia o serviço focado na aba atual
			currentTab := m.tabs[m.activeTab]
			if currentTab != "ALL" && currentTab != "📊 MÉTRICAS" && m.restartFunc != nil {
				m.statusMsg = fmt.Sprintf("Reiniciando serviço '%s'...", currentTab)
				go func(svc string) {
					_ = m.restartFunc(svc)
				}(currentTab)
			}

		case "c":
			// Limpa logs da aba ativa
			currentTab := m.tabs[m.activeTab]
			if currentTab != "📊 MÉTRICAS" {
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
		switch msg.Type {
		case tea.MouseWheelUp:
			m.viewport.LineUp(3)
		case tea.MouseWheelDown:
			m.viewport.LineDown(3)
		case tea.MouseLeft:
			// Clique nas abas
			if msg.Y >= 3 && msg.Y <= 6 {
				xOffset := 1
				for i, tab := range m.tabs {
					tabWidth := len(tab) + 4
					if msg.X >= xOffset && msg.X <= xOffset+tabWidth {
						m.activeTab = i
						m.syncViewport()
						break
					}
					xOffset += tabWidth
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 7
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
		if m.tabs[m.activeTab] == "📊 MÉTRICAS" {
			m.syncViewport()
		}

	case domain.PortRemapped:
		if card, ok := m.cards[msg.Service]; ok {
			card.OriginalPort = msg.OriginalPort
			card.Port = msg.TargetPort
			card.IsRemapped = true
		}
		if m.tabs[m.activeTab] == "📊 MÉTRICAS" {
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
		if m.tabs[m.activeTab] == "📊 MÉTRICAS" {
			m.syncViewport()
		}
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *AppModel) syncViewport() {
	currentTab := m.tabs[m.activeTab]
	if currentTab == "📊 MÉTRICAS" {
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

	b.WriteString(headerStyle.Render(" PAINEL COMPLETO DE TELEMETRIA E RECURSOS DO SISTEMA ") + "\n\n")

	metricBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 2).
		MarginBottom(1)

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#C0C0C0")).Width(24)
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)

	for _, name := range m.services {
		card := m.cards[name]

		var content strings.Builder
		content.WriteString(lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(fmt.Sprintf("SERVIÇO: %s", strings.ToUpper(name))) + "\n")
		content.WriteString(lipgloss.NewStyle().Foreground(subtleColor).Render(strings.Repeat("─", 65)) + "\n")

		// Status
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
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Status Operacional:"), fmt.Sprintf("%s %s (%s)", icon, card.State, card.Detail)))

		// Porta
		portStr := "nenhuma porta declarada"
		if card.Port > 0 {
			if card.IsRemapped {
				portStr = fmt.Sprintf("%d ➔ %d [REMAPEAMENTO ATIVO]", card.OriginalPort, card.Port)
			} else {
				portStr = fmt.Sprintf("%d (estável)", card.Port)
			}
		}
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Porta de Rede (TCP):"), valStyle.Render(portStr)))

		// CPU
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Uso de Processador:"), valStyle.Render(telemetry.FormatCPU(card.CPUPercent))))

		// Memória RAM
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Uso de Memória RAM:"), valStyle.Render(fmt.Sprintf("%s (Resident Set Size)", telemetry.FormatBytes(card.MemoryBytes)))))

		// E/S de Disco
		diskStr := fmt.Sprintf("Leitura: %s  •  Escrita: %s", telemetry.FormatBytes(card.DiskReadBytes), telemetry.FormatBytes(card.DiskWriteBytes))
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("E/S de Disco (I/O):"), valStyle.Render(diskStr)))

		// Tempo de Resposta
		probeStr := "medindo latência do probe..."
		if card.Detail != "" && strings.Contains(card.Detail, "latência:") {
			probeStr = card.Detail
		}
		content.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Tempo de Resposta:"), valStyle.Render(probeStr)))

		b.WriteString(metricBox.Render(content.String()) + "\n")
	}

	return b.String()
}

func (m *AppModel) View() string {
	if !m.ready {
		return "\n  Iniciando vigiaDev TUI..."
	}

	var b strings.Builder

	// 1. Cabeçalho
	header := titleStyle.Render(fmt.Sprintf(" vigiaDev — %s ", m.ProjectName))
	b.WriteString(header + "\n\n")

	// 2. Cards Superiores Limpos e Elegantes (Status + Porta)
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

		cardContent := fmt.Sprintf("%s %s%s\n%s",
			icon,
			lipgloss.NewStyle().Bold(true).Render(card.Name),
			portInfo,
			lipgloss.NewStyle().Foreground(stateColor).Render(string(card.State)),
		)

		cardsRow.WriteString(cardBorder.Render(cardContent))
	}
	b.WriteString(cardsRow.String() + "\n\n")

	// 3. Abas
	var tabsRow strings.Builder
	for i, tab := range m.tabs {
		tabLabel := fmt.Sprintf("%d:%s", i+1, tab)
		if tab != "📊 MÉTRICAS" {
			count := len(m.logs[tab])
			tabLabel = fmt.Sprintf("%d:%s (%d)", i+1, tab, count)
		}

		if i == m.activeTab {
			tabsRow.WriteString(activeTabStyle.Render(tabLabel))
		} else {
			tabsRow.WriteString(inactiveTabStyle.Render(tabLabel))
		}
	}
	b.WriteString(tabsRow.String() + "\n")

	// 4. Viewport de Logs ou Dashboard de Métricas
	b.WriteString(m.viewport.View() + "\n")

	// 5. Rodapé Interativo com ação contextual
	currentTab := m.tabs[m.activeTab]
	var footerText string
	if currentTab != "ALL" && currentTab != "📊 MÉTRICAS" {
		footerText = fmt.Sprintf("[Tab/Clique] Abas  •  [r] REINICIAR '%s'  •  [c] Limpar Logs  •  [q] Sair", currentTab)
	} else if currentTab == "📊 MÉTRICAS" {
		footerText = "[Tab/Clique] Alternar Abas  •  [Scroll/Setas] Rolar Métricas  •  [q] Sair"
	} else {
		footerText = "[Tab/Clique] Alternar Abas  •  [1-9] Atalho Numérico  •  [Scroll/Setas] Rolar  •  [q] Sair"
	}

	b.WriteString(footerStyle.Render(footerText))

	return b.String()
}

// RunTUI inicia o programa Bubble Tea conectando os eventos e o callback de reinício de serviço.
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
