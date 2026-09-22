package domain

import (
	"errors"
	"fmt"
)

var (
	ErrServiceNotFound    = errors.New("serviço não encontrado na configuração")
	ErrCircularDependency = errors.New("dependência circular detectada no grafo (DAG)")
	ErrStaleLockDetected  = errors.New("sessão anterior parece ter caído abruptamente (lock órfão)")
	ErrSessionLocked      = errors.New("outra instância do vigiaDev já está em execução neste diretório")
)

// PortConflictError descreve conflito de alocação de portas no host.
type PortConflictError struct {
	Service string
	Port    int
	Message string
}

func (e *PortConflictError) Error() string {
	return fmt.Sprintf("conflito na porta %d para o serviço '%s': %s", e.Port, e.Service, e.Message)
}
