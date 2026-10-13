package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
)

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

