package application

import (
	"fmt"
	"sort"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// DAGPlan contém a ordenação linear e as ondas de execução paralela calculadas pelo algoritmo.
type DAGPlan struct {
	// LinearOrder é a sequência topológica completa (ex: ["db", "backend", "frontend"])
	LinearOrder []string
	// Waves são grupos de serviços que podem ser iniciados concorrentemente
	Waves [][]string
}

// BuildDAGPlan calcula o plano de execução a partir dos serviços declarados no vigiadev.yaml.
// Implementa o algoritmo de Kahn para ordenação topológica e agrupamento em níveis (waves).
func BuildDAGPlan(services map[string]domain.ServiceConfig) (*DAGPlan, error) {
	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	// 1. Inicializa todos os nós
	for name := range services {
		inDegree[name] = 0
		adjList[name] = make([]string, 0)
	}

	// 2. Constrói as arestas e calcula o grau de entrada (in-degree)
	for name, svc := range services {
		for _, dep := range svc.DependsOn {
			if _, exists := services[dep]; !exists {
				return nil, fmt.Errorf("%w: serviço '%s' depende de '%s' que não existe", domain.ErrServiceNotFound, name, dep)
			}
			// dep precisa rodar antes de name: dep -> name
			adjList[dep] = append(adjList[dep], name)
			inDegree[name]++
		}
	}

	// 3. Algoritmo de Kahn com agrupamento em ondas
	var waves [][]string
	var linearOrder []string

	for {
		// Encontra todos os nós com in-degree 0 nesta rodada
		var currentWave []string
		for name, degree := range inDegree {
			if degree == 0 {
				currentWave = append(currentWave, name)
			}
		}

		if len(currentWave) == 0 {
			break
		}

		// Ordenação alfabética determinística para reprodutibilidade
		sort.Strings(currentWave)
		waves = append(waves, currentWave)

		// Remove os nós processados e decrementa o grau dos dependentes
		for _, node := range currentWave {
			linearOrder = append(linearOrder, node)
			delete(inDegree, node)

			for _, dependent := range adjList[node] {
				inDegree[dependent]--
			}
		}
	}

	// 4. Se ainda restaram nós no mapa, temos um ciclo!
	if len(inDegree) > 0 {
		var cycleNodes []string
		for name := range inDegree {
			cycleNodes = append(cycleNodes, name)
		}
		sort.Strings(cycleNodes)
		return nil, fmt.Errorf("%w: ciclo envolvendo os serviços: %v", domain.ErrCircularDependency, cycleNodes)
	}

	return &DAGPlan{
		LinearOrder: linearOrder,
		Waves:       waves,
	}, nil
}
