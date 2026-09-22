package application_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestBuildDAGPlan_Linear(t *testing.T) {
	services := map[string]domain.ServiceConfig{
		"postgres": {},
		"backend":  {DependsOn: []string{"postgres"}},
		"frontend": {DependsOn: []string{"backend"}},
	}

	plan, err := application.BuildDAGPlan(services)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	expectedLinear := []string{"postgres", "backend", "frontend"}
	if !reflect.DeepEqual(plan.LinearOrder, expectedLinear) {
		t.Errorf("esperava ordem %v, obteve %v", expectedLinear, plan.LinearOrder)
	}

	expectedWaves := [][]string{
		{"postgres"},
		{"backend"},
		{"frontend"},
	}
	if !reflect.DeepEqual(plan.Waves, expectedWaves) {
		t.Errorf("esperava ondas %v, obteve %v", expectedWaves, plan.Waves)
	}
}

func TestBuildDAGPlan_BranchingWaves(t *testing.T) {
	services := map[string]domain.ServiceConfig{
		"postgres": {},
		"redis":    {},
		"backend":  {DependsOn: []string{"postgres", "redis"}},
		"worker":   {DependsOn: []string{"redis"}},
		"frontend": {DependsOn: []string{"backend"}},
	}

	plan, err := application.BuildDAGPlan(services)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	expectedWaves := [][]string{
		{"postgres", "redis"}, // Ambos sem dependências
		{"backend", "worker"},   // postgres e redis prontos
		{"frontend"},           // backend pronto
	}

	if !reflect.DeepEqual(plan.Waves, expectedWaves) {
		t.Errorf("esperava ondas %v, obteve %v", expectedWaves, plan.Waves)
	}
}

func TestBuildDAGPlan_CircularDependency(t *testing.T) {
	services := map[string]domain.ServiceConfig{
		"servicoA": {DependsOn: []string{"servicoB"}},
		"servicoB": {DependsOn: []string{"servicoA"}},
	}

	_, err := application.BuildDAGPlan(services)
	if err == nil {
		t.Fatal("esperava erro de dependência circular, mas teve sucesso")
	}

	if !errors.Is(err, domain.ErrCircularDependency) {
		t.Errorf("esperava ErrCircularDependency, obteve %v", err)
	}
}

func TestBuildDAGPlan_MissingDependency(t *testing.T) {
	services := map[string]domain.ServiceConfig{
		"backend": {DependsOn: []string{"banco_inexistente"}},
	}

	_, err := application.BuildDAGPlan(services)
	if err == nil {
		t.Fatal("esperava erro de dependência inexistente, mas teve sucesso")
	}

	if !errors.Is(err, domain.ErrServiceNotFound) {
		t.Errorf("esperava ErrServiceNotFound, obteve %v", err)
	}
}
