package ports

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

const MaxScanAttempts = 50

// PortResolver gerencia a verificação de portas no host e remapeamento sequencial determinístico.
type PortResolver struct {
	Host string
}

// NewPortResolver instancia o resolvedor com o host padrão (127.0.0.1).
func NewPortResolver(host string) *PortResolver {
	if host == "" {
		host = "127.0.0.1"
	}
	return &PortResolver{Host: host}
}

// IsPortAvailable testa se a porta informada pode ser vinculada (bind) no host.
// Retorna true se a porta estiver livre, ou false se já estiver em uso.
func (pr *PortResolver) IsPortAvailable(port int) bool {
	addr := net.JoinHostPort(pr.Host, strconv.Itoa(port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// FindAvailablePort procura a próxima porta livre a partir de startPort + 1,
// respeitando o limite determinístico de maxAttempts (padrão 50, DEC-012).
func (pr *PortResolver) FindAvailablePort(startPort int, maxAttempts int) (int, error) {
	if maxAttempts <= 0 {
		maxAttempts = MaxScanAttempts
	}

	for i := 1; i <= maxAttempts; i++ {
		candidate := startPort + i
		if candidate > 65535 {
			break
		}
		if pr.IsPortAvailable(candidate) {
			return candidate, nil
		}
	}

	return 0, &domain.PortConflictError{
		Port:    startPort,
		Message: fmt.Sprintf("nenhuma porta livre encontrada nas %d tentativas sequenciais após a porta %d", maxAttempts, startPort),
	}
}

// InterpolatePort substitui ocorrências de {port} pelo número da porta informada.
func InterpolatePort(text string, port int) string {
	return strings.ReplaceAll(text, "{port}", strconv.Itoa(port))
}

// InterpolateCommand substitui {port} em cada argumento da fatia de comando.
func InterpolateCommand(args []string, port int) []string {
	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = InterpolatePort(arg, port)
	}
	return result
}

// InterpolateEnv substitui {port} nos valores do mapa de variáveis de ambiente.
func InterpolateEnv(env map[string]string, port int) map[string]string {
	result := make(map[string]string, len(env))
	for k, v := range env {
		result[k] = InterpolatePort(v, port)
	}
	return result
}
