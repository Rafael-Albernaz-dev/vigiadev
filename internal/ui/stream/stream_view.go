package stream

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// Cores ANSI básicas sem dependências externas
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorGray   = "\033[90m"
)

// StreamView consome eventos do EventBus e imprime logs coloridos em modo streaming.
type StreamView struct {
	out        io.Writer
	mu         sync.Mutex
	colorIndex int
	colors     map[string]string
}

// NewStreamView cria uma nova visualização de terminal em modo streaming.
func NewStreamView(out io.Writer) *StreamView {
	if out == nil {
		out = os.Stdout
	}
	return &StreamView{
		out:    out,
		colors: make(map[string]string),
	}
}

// Attach subscreve o StreamView ao EventBus informado.
func (sv *StreamView) Attach(bus *domain.EventBus) {
	bus.Subscribe(func(event domain.Event) {
		sv.handleEvent(event)
	})
}

func (sv *StreamView) handleEvent(event domain.Event) {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	switch e := event.(type) {
	case domain.LogLineProduced:
		color := sv.getServiceColor(e.Service)
		prefix := fmt.Sprintf("%s[%s]%s", color, e.Service, ColorReset)
		if e.IsError {
			_, _ = fmt.Fprintf(sv.out, "%s %s%s%s\n", prefix, ColorRed, e.Line, ColorReset)
		} else {
			_, _ = fmt.Fprintf(sv.out, "%s %s\n", prefix, e.Line)
		}

	case domain.PortRemapped:
		_, _ = fmt.Fprintf(sv.out, "%s🔄 [REMAP] %s: port %d occupied ➔ remapped to %d%s\n",
			ColorYellow, e.Service, e.OriginalPort, e.TargetPort, ColorReset)

	case domain.ServiceStateChanged:
		icon := "⚪"
		switch e.NewState {
		case domain.StateHealthy:
			icon = "🟢"
		case domain.StateStarting:
			icon = "🟡"
		case domain.StateFailed:
			icon = "🔴"
		case domain.StateStopped:
			icon = "⏹️"
		}
		_, _ = fmt.Fprintf(sv.out, "%s [%s] %s (%s)\n", icon, e.Service, e.NewState, e.Detail)
	}
}

func (sv *StreamView) getServiceColor(service string) string {
	if c, ok := sv.colors[service]; ok {
		return c
	}
	palette := []string{ColorCyan, ColorGreen, ColorBlue, ColorYellow}
	color := palette[sv.colorIndex%len(palette)]
	sv.colorIndex++
	sv.colors[service] = color
	return color
}
