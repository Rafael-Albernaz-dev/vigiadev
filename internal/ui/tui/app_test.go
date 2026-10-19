package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestAppModel_AtlasSizedTerminalNavigation(t *testing.T) {
	names := []string{"mailpit", "minio", "postgres", "redis", "zitadel", "zitadel-login", "zitadel-postgres", "erp-backend", "minio-init", "nginx", "platform-backend", "viabilidade-backend", "erp-frontend", "platform-frontend", "viabilidade-frontend"}
	links := []domain.LinkConfig{
		{Name: "Entrada principal — Platform", URL: "http://platform.atlas.localhost:8080/"},
		{Name: "Catálogo de sistemas", URL: "http://platform.atlas.localhost:8080/systems"},
		{Name: "Inventário de localizações", URL: "http://platform.atlas.localhost:8080/inventory/locations"},
		{Name: "Configurações Rádio", URL: "http://platform.atlas.localhost:8080/settings/viabilidade/radio"},
		{Name: "Atlas Viabilidade (origem canônica)", URL: "http://viabilidade.atlas.localhost:8080/"},
		{Name: "Zitadel Console / Login", URL: "http://localhost:8080/ui/v2/login/"},
	}
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {160, 50}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			ports := make(map[string]int)
			for i, name := range names {
				ports[name] = 3000 + i
			}
			m := NewAppModel("atlas", names, ports, domain.NewEventBus(), func() {}, nil)
			m.SetNavigation(map[string]domain.ServiceConfig{
				"platform-frontend":    {URL: links[0].URL},
				"viabilidade-frontend": {URL: links[4].URL},
			}, links)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			checkFrame := func() string {
				t.Helper()
				view := m.View()
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
					t.Fatalf("frame %dx%d exceeds terminal %dx%d", lipgloss.Width(view), lipgloss.Height(view), size[0], size[1])
				}
				return view
			}
			allPages := checkFrame()
			if m.tabRowYEnd <= m.tabRowYStart {
				t.Fatal("Atlas tabs were not wrapped")
			}
			for m.pageOffset < m.pageOverflow {
				m.Update(tea.KeyMsg{Type: tea.KeyDown})
				allPages += checkFrame()
			}
			for _, link := range links {
				if !strings.Contains(allPages, "\x1b]8;;"+link.URL+"\x1b\\") {
					t.Fatalf("shortcut unreachable by scrolling: %s", link.URL)
				}
			}
			// A click on a wrapped tab must use both its row and scroll offset.
			zone := m.tabZones[len(names)-1] // platform-frontend
			y := zone.rowY
			if m.pageOverflow > 0 {
				y = y - m.pageOffset + 1
			}
			if y < 0 || y >= size[1] {
				t.Fatalf("frontend tab not reachable: y=%d", y)
			}
			m.Update(tea.MouseMsg{X: zone.startX + 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			if m.tabs[m.activeTab] != "platform-frontend" {
				t.Fatalf("clicked wrong wrapped tab: %s", m.tabs[m.activeTab])
			}
			view := checkFrame()
			if !strings.Contains(view, formatClickableURL(links[0].URL, links[0].URL)) {
				t.Fatal("canonical frontend URL missing")
			}
			m.diagnosticsOpen = true
			m.pageOffset = 0
			checkFrame()
			m.Update(tea.KeyMsg{Type: tea.KeyEnd})
			if view := checkFrame(); !strings.Contains(view, formatClickableURL(links[4].URL, links[4].URL)) {
				t.Fatal("last service navigation URL unreachable in diagnostics")
			}
		})
	}
}

func TestAppModel_DeclaredNavigationAndShortcuts(t *testing.T) {
	canonical := "http://viabilidade.atlas.localhost:8080/"
	shortcut := "http://platform.atlas.localhost:8080/inventory/locations"
	bus := domain.NewEventBus()
	model := NewAppModel("atlas", []string{"web", "portal", "postgres"}, map[string]int{"web": 5175, "postgres": 5435}, bus, func() {}, nil)
	model.SetNavigation(map[string]domain.ServiceConfig{
		"web": {URL: canonical}, "portal": {URL: "https://example.com/portal"},
	}, []domain.LinkConfig{{Name: "Inventário de localizações", URL: shortcut}})
	model.cards["web"].Detail = strings.Repeat("health detail ", 8)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 55})
	link := "\x1b]8;;" + canonical + "\x1b\\" + canonical + "\x1b]8;;\x1b\\"
	view := model.View()
	for _, want := range []string{link, "NAVIGATION SHORTCUTS", "Inventário de localizações", formatClickableURL(shortcut, shortcut), ":5435", formatClickableURL("https://example.com/portal", "https://example.com/portal")} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing navigation %q in ALL: %s", want, view)
		}
	}
	if strings.Contains(view, "http://localhost:5175") || strings.Contains(view, "http://localhost:5435") {
		t.Fatal("declared URL or database fallback was replaced by an internal HTTP URL")
	}
	if lipgloss.Width(link) != lipgloss.Width(canonical) || ansi.Strip(link) != canonical {
		t.Fatal("OSC 8 escapes contributed to cell width")
	}
	// Both service rows must occupy a single physical line despite different URL lengths.
	var webY, portalY int
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, canonical) {
			webY = i
		}
		if strings.Contains(line, "https://example.com/portal") {
			portalY = i
		}
	}
	if portalY != webY+1 {
		t.Fatalf("URL wrapped the service rows: web=%d portal=%d", webY, portalY)
	}
	before := model.viewport.Height
	model.activeTab = 1
	view = model.View()
	if !strings.Contains(view, "🌐 "+link) || strings.Contains(view, "NAVIGATION SHORTCUTS") || model.viewport.Height <= before {
		t.Fatalf("individual footer or shortcut viewport sizing incorrect: %s", view)
	}
	model.Update(domain.PortRemapped{Service: "web", OriginalPort: 5175, TargetPort: 5176})
	if model.cards["web"].Port != 5176 || model.cards["web"].URL != canonical || !strings.Contains(model.View(), link) {
		t.Fatal("remapping changed the canonical navigation URL")
	}
	model.Update(domain.HealthCheckProbed{Service: "web", Type: domain.HealthCheckTCP, Target: "localhost:5176", Success: true})
	model.diagnosticsOpen = true
	diagnostics := model.View()
	if !strings.Contains(diagnostics, "Navigation: "+link) || !strings.Contains(diagnostics, "localhost:5176") {
		t.Fatalf("diagnostics must retain navigation AND operational target: %s", diagnostics)
	}
}

func TestAppModel_NoShortcutsForLegacyConfig(t *testing.T) {
	model := NewAppModel("legacy", []string{"web"}, map[string]int{"web": 3000}, domain.NewEventBus(), func() {}, nil)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if view := model.View(); strings.Contains(view, "NAVIGATION SHORTCUTS") || !strings.Contains(view, formatClickableURL("http://localhost:3000", "http://localhost:3000")) {
		t.Fatalf("legacy navigation changed: %s", view)
	}
}

func TestAppModel_InitialPortsAndURLRendering(t *testing.T) {
	bus := domain.NewEventBus()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	serviceNames := []string{"postgres", "redis", "viabilidade-backend", "viabilidade-frontend"}
	servicePorts := map[string]int{
		"postgres":             5432,
		"redis":                6379,
		"viabilidade-backend":  3175,
		"viabilidade-frontend": 5175,
	}

	model := NewAppModel("test-atlas", serviceNames, servicePorts, bus, cancel, nil)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(*AppModel)

	// Verifica se os cards foram inicializados com as portas
	for name, expectedPort := range servicePorts {
		card, ok := model.cards[name]
		if !ok {
			t.Fatalf("card %s não encontrado", name)
		}
		if card.Port != expectedPort {
			t.Errorf("card %s: esperava porta %d, obteve %d", name, expectedPort, card.Port)
		}
		if card.OriginalPort != expectedPort {
			t.Errorf("card %s: esperava OriginalPort %d, obteve %d", name, expectedPort, card.OriginalPort)
		}
	}

	// Renderiza a tela
	rendered := model.View()

	// 1. Serviços de banco/TCP devem renderizar :5432 e :6379 (não http://...)
	if !strings.Contains(rendered, ":5432") {
		t.Errorf("esperava ':5432' na visualização, obteve:\n%s", rendered)
	}
	if !strings.Contains(rendered, ":6379") {
		t.Errorf("esperava ':6379' na visualização, obteve:\n%s", rendered)
	}

	// 2. Serviços web devem renderizar com sequência ANSI OSC 8
	expectedOSC8Frontend := formatClickableURL("http://localhost:5175", "http://localhost:5175")
	if !strings.Contains(rendered, expectedOSC8Frontend) {
		t.Errorf("esperava hiperlink OSC 8 para frontend, obteve:\n%s", rendered)
	}

	expectedOSC8Backend := formatClickableURL("http://localhost:3175", "http://localhost:3175")
	if !strings.Contains(rendered, expectedOSC8Backend) {
		t.Errorf("esperava hiperlink OSC 8 para backend, obteve:\n%s", rendered)
	}
}

func TestAppModel_PortRemappedRendering(t *testing.T) {
	bus := domain.NewEventBus()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	serviceNames := []string{"web"}
	servicePorts := map[string]int{"web": 3000}

	model := NewAppModel("test-remap", serviceNames, servicePorts, bus, cancel, nil)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(*AppModel)

	// Simula evento de remapeamento
	remapEvent := domain.PortRemapped{
		BaseEvent:    domain.NewBaseEvent(),
		Service:      "web",
		OriginalPort: 3000,
		TargetPort:   3001,
	}
	updatedModel, _ := model.Update(remapEvent)
	m := updatedModel.(*AppModel)

	card := m.cards["web"]
	if card.Port != 3001 || card.OriginalPort != 3000 || !card.IsRemapped {
		t.Fatalf("estado do card após remap inválido: %+v", card)
	}

	rendered := m.View()
	expectedText := "http://localhost:3000➔:3001"
	expectedOSC8 := formatClickableURL("http://localhost:3001", expectedText)
	if !strings.Contains(rendered, expectedOSC8) {
		t.Errorf("esperava hiperlink remapeado OSC 8 '%s', obteve:\n%s", expectedOSC8, rendered)
	}
}

func TestAppModel_FooterURLRendering(t *testing.T) {
	bus := domain.NewEventBus()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	serviceNames := []string{"viabilidade-frontend", "postgres"}
	servicePorts := map[string]int{
		"viabilidade-frontend": 5175,
		"postgres":             5432,
	}

	model := NewAppModel("test-footer", serviceNames, servicePorts, bus, cancel, nil)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(*AppModel)

	// Ativa aba 1: viabilidade-frontend
	model.activeTab = 1
	viewFrontend := model.View()
	if !strings.Contains(viewFrontend, "🌐") || !strings.Contains(viewFrontend, "http://localhost:5175") {
		t.Errorf("rodapé para aba frontend não contém URL 🌐: %s", viewFrontend)
	}

	// Ativa aba 2: postgres
	model.activeTab = 2
	viewDB := model.View()
	if !strings.Contains(viewDB, "🔌 localhost:5432") {
		t.Errorf("rodapé para aba postgres não contém 🔌 localhost:5432: %s", viewDB)
	}
}

func TestAppModel_RestartKeyHandling(t *testing.T) {
	bus := domain.NewEventBus()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	var restartedService string
	restartFunc := func(service string) error {
		restartedService = service
		return nil
	}

	model := NewAppModel("test-restart", []string{"api", "web"}, map[string]int{"api": 3000, "web": 5000}, bus, cancel, restartFunc)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(*AppModel)

	// 1. Pressionar 'r' na aba ALL (índice 0) deve dar mensagem de ajuda no statusMsg e NÃO reiniciar
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(*AppModel)
	if cmd != nil {
		t.Errorf("esperava cmd nil na aba ALL, obteve %v", cmd)
	}
	if !strings.Contains(model.statusMsg, "Select a specific service tab") {
		t.Errorf("esperava mensagem no statusMsg informando para selecionar aba de serviço, obteve: %q", model.statusMsg)
	}
	if restartedService != "" {
		t.Errorf("não deveria ter reiniciado nenhum serviço na aba ALL")
	}

	// 2. Mudar para a aba 'api' (índice 1) e pressionar 'R' maiúsculo
	model.activeTab = 1
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	model = updated.(*AppModel)
	if cmd == nil {
		t.Fatal("esperava comando de reinício para 'R' maiúsculo na aba 'api'")
	}
	_ = cmd()
	if restartedService != "api" {
		t.Errorf("esperava reinício de 'api', obteve %q", restartedService)
	}

	// 3. Abrir modal de diagnósticos e pressionar 'r'
	restartedService = ""
	model.diagnosticsOpen = true
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(*AppModel)
	if model.diagnosticsOpen {
		t.Errorf("modal de diagnósticos deveria ter fechado após pressionar 'r'")
	}
	if cmd == nil {
		t.Fatal("esperava comando de reinício ao pressionar 'r' dentro do modal de diagnósticos")
	}
	_ = cmd()
	if restartedService != "api" {
		t.Errorf("esperava reinício de 'api' vindo do modal de diagnósticos, obteve %q", restartedService)
	}
}
