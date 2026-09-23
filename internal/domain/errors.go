package domain

import (
	"errors"
	"fmt"
)

var (
	ErrServiceNotFound    = errors.New("service not found in configuration")
	ErrCircularDependency = errors.New("circular dependency detected in DAG")
	ErrStaleLockDetected  = errors.New("previous session terminated abruptly (stale lock detected)")
	ErrSessionLocked      = errors.New("another vigiaDev instance is already running in this directory")
)

// PortConflictError describes a host port conflict.
type PortConflictError struct {
	Service string
	Port    int
	Message string
}

func (e *PortConflictError) Error() string {
	return fmt.Sprintf("conflict on port %d for service '%s': %s", e.Port, e.Service, e.Message)
}
