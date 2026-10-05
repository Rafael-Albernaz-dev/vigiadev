package ports_test

import (
	"net"
	"reflect"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestIsPortAvailable(t *testing.T) {
	resolver := ports.NewPortResolver("127.0.0.1")

	// Abre um socket TCP real em porta efêmera (porta 0 = SO escolhe uma livre)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("falha ao abrir socket de teste: %v", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port

	// Com o socket aberto, IsPortAvailable deve retornar false
	if resolver.IsPortAvailable(port) {
		t.Fatalf("esperava que a porta ocupada %d retornasse false", port)
	}

	// Fecha o socket
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	// Agora a porta deve estar livre
	if !resolver.IsPortAvailable(port) {
		t.Fatalf("esperava que a porta liberada %d retornasse true", port)
	}
}

func TestFindAvailablePort_Sequential(t *testing.T) {
	resolver := ports.NewPortResolver("127.0.0.1")

	// Abre um listener na porta base
	listenerBase, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listenerBase.Close()

	basePort := listenerBase.Addr().(*net.TCPAddr).Port

	// Busca a próxima disponível a partir da basePort
	nextPort, err := resolver.FindAvailablePort(basePort, 10)
	if err != nil {
		t.Fatalf("falha ao encontrar porta: %v", err)
	}

	if nextPort <= basePort {
		t.Fatalf("esperava porta maior que %d, obteve %d", basePort, nextPort)
	}
}

func TestInterpolations(t *testing.T) {
	// 1. Comando
	args := []string{"python3", "-m", "http.server", "{port}", "--bind", "127.0.0.1"}
	expectedArgs := []string{"python3", "-m", "http.server", "8001", "--bind", "127.0.0.1"}
	interpolatedArgs := ports.InterpolateCommand(args, 8001)

	if !reflect.DeepEqual(interpolatedArgs, expectedArgs) {
		t.Errorf("esperava %v, obteve %v", expectedArgs, interpolatedArgs)
	}

	// 2. Variáveis de ambiente
	env := map[string]string{
		"PORT":    "{port}",
		"API_URL": "http://localhost:{port}/v1",
		"STATIC":  "constante",
	}
	expectedEnv := map[string]string{
		"PORT":    "3000",
		"API_URL": "http://localhost:3000/v1",
		"STATIC":  "constante",
	}
	interpolatedEnv := ports.InterpolateEnv(env, 3000)

	if !reflect.DeepEqual(interpolatedEnv, expectedEnv) {
		t.Errorf("esperava %v, obteve %v", expectedEnv, interpolatedEnv)
	}
}

func TestRemapServiceCopiesAndInterpolates(t *testing.T) {
	original := domain.ServiceConfig{Ports: []int{3000}, Command: []string{"server", "{port}"}, Env: map[string]string{"PORT": "3000"}, HealthCheck: &domain.HealthCheckConfig{Port: 3000, Command: []string{"probe", "{port}"}, URL: "http://localhost:{port}"}}
	assigned := ports.RemapService(original, 0, 3001)
	if assigned.Env["PORT"] != "3001" || assigned.HealthCheck.Command[1] != "3001" || assigned.HealthCheck.URL != "http://localhost:3001" {
		t.Fatalf("incomplete remap: %+v", assigned)
	}
	if original.Ports[0] != 3000 || original.Env["PORT"] != "3000" || original.Command[1] != "{port}" || original.HealthCheck.Port != 3000 {
		t.Fatal("remap mutated input")
	}
	noEnv := ports.RemapService(domain.ServiceConfig{Ports: []int{3000}}, 0, 3001)
	if noEnv.Env["PORT"] != "3001" {
		t.Fatal("missing PORT with nil env")
	}
}
