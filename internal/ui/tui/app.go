package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// ServiceCardState armazena os metadados visuais de cada serviço no card superior.
type ServiceCardState struct {
	Name         string
	State        domain.ServiceState
	Port         int
	OriginalPort int
	IsRemapped   bool
	Detail       string
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
}

// NewAppModel instancia o modelo da TUI interativa.
func NewAppModel(projectName string, serviceNames []string, bus *domain.EventBus, cancel context.CancelFunc) *AppModel {
	tabs := append([]string{"ALL"}, serviceNames...)
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

		case "c":
			// Limpa logs da aba ativa
			currentTab := m.tabs[m.activeTab]
			m.logs[currentTab] = make([]string, 0)
			m.syncViewport()

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0]-'1')
			if idx < len(m.tabs) {
				m.activeTab = idx
				m.syncViewport()
			}
		}

	case tea.MouseMsg:
		// Suporte a cliques do mouse e rolagem da roda
		switch msg.Type {
		case tea.MouseWheelUp:
			m.viewport.LineUp(3)
		case tea.MouseWheelDown:
			m.viewport.LineDown(3)
		case tea.MouseLeft:
			// Clique nas abas (linha 4 aproximadamente)
			if msg.Y >= 3 && msg.Y <= 5 {
				xOffset := 1
				for i, tab := range m.tabs {
					tabWidth := len(tab) + 4 // padding + margin
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

		// Adiciona ao buffer ALL e ao buffer específico do serviço
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

	case domain.PortRemapped:
		if card, ok := m.cards[msg.Service]; ok {
			card.OriginalPort = msg.OriginalPort
			card.Port = msg.TargetPort
			card.IsRemapped = true
		}
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *AppModel) syncViewport() {
	currentTab := m.tabs[m.activeTab]
	lines := m.logs[currentTab]
	content := strings.Join(lines, "\n")
	m.viewport.SetContent(content)
}

func (m *AppModel) View() string {
	if !m.ready {
		return "\n  Iniciando vigiaDev TUI..."
	}

	var b strings.Builder

	// 1. Cabeçalho
	header := titleStyle.Render(fmt.Sprintf(" vigiaDev — %s ", m.ProjectName))
	b.WriteString(header + "\n\n")

	// 2. Cards Superiores de Status
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

	// 3. Abas com suporte visual a clique
	var tabsRow strings.Builder
	for i, tab := range m.tabs {
		count := len(m.logs[tab])
		tabLabel := fmt.Sprintf("%d:%s (%d)", i+1, tab, count)

		if i == m.activeTab {
			tabsRow.WriteString(activeTabStyle.Render(tabLabel))
		} else {
			tabsRow.WriteString(inactiveTabStyle.Render(tabLabel))
		}
	}
	b.WriteString(tabsRow.String() + "\n")

	// 4. Viewport de Logs
	b.WriteString(m.viewport.View() + "\n")

	// 5. Rodapé interativo
	footer := footerStyle.Render(
		"[Tab/Clique] Alternar Abas  •  [Scroll/Setas] Rolar  •  [c] Limpar Tela  •  [q] Sair e Teardown",
	)
	b.WriteString(footer)

	return b.String()
}

// RunTUI inicia o programa Bubble Tea conectando os eventos do EventBus ao modelo.
func RunTUI(ctx context.Context, projectName string, serviceNames []string, bus *domain.EventBus, cancel context.CancelFunc) error {
	model := NewAppModel(projectName, serviceNames, bus, cancel)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Ativa mouse, cliques e rolagem da roda
	)

	// Encaminha eventos do EventBus para o canal de mensagens thread-safe do Bubble Tea
	bus.Subscribe(func(event domain.Event) {
		p.Send(event)
	})

	// Se o contexto for cancelado externamente, encerra a TUI
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, err := p.Run()
	return err
}
