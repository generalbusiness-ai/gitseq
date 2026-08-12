package perflane

import (
	"errors"
	"fmt"
)

// Evidence distinguishes a measured zero value from an unavailable value.
// Exactly one of Value and UnavailableReason must be present.
type Evidence[T any] struct {
	Value             *T     `json:"value"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

func Available[T any](value T) Evidence[T] { return Evidence[T]{Value: &value} }

func Unavailable[T any](reason string) Evidence[T] {
	return Evidence[T]{UnavailableReason: reason}
}

func (e Evidence[T]) Validate() error {
	if e.Value != nil && e.UnavailableReason != "" {
		return errors.New("evidence cannot have both a value and an unavailable reason")
	}
	if e.Value == nil && e.UnavailableReason == "" {
		return errors.New("evidence must have a value or an unavailable reason")
	}
	return nil
}

// EnvironmentEvidence records the environment fields needed to compare runs.
// Collectors must mark unsupported fields unavailable instead of using zero.
type EnvironmentEvidence struct {
	OS              Evidence[string] `json:"os"`
	Architecture    Evidence[string] `json:"architecture"`
	GoVersion       Evidence[string] `json:"go_version"`
	GitVersion      Evidence[string] `json:"git_version"`
	CPUModel        Evidence[string] `json:"cpu_model"`
	LogicalCPUs     Evidence[int]    `json:"logical_cpus"`
	MemoryBytes     Evidence[uint64] `json:"memory_bytes"`
	ContainerCPU    Evidence[string] `json:"container_cpu_limit"`
	ContainerMemory Evidence[uint64] `json:"container_memory_limit_bytes"`
	Filesystem      Evidence[string] `json:"filesystem"`
	PowerMode       Evidence[string] `json:"power_mode"`
	DirtyWorktree   Evidence[bool]   `json:"dirty_worktree"`
}

func (e EnvironmentEvidence) Validate() error {
	fields := []struct {
		name string
		err  error
	}{
		{"os", e.OS.Validate()},
		{"architecture", e.Architecture.Validate()},
		{"go_version", e.GoVersion.Validate()},
		{"git_version", e.GitVersion.Validate()},
		{"cpu_model", e.CPUModel.Validate()},
		{"logical_cpus", e.LogicalCPUs.Validate()},
		{"memory_bytes", e.MemoryBytes.Validate()},
		{"container_cpu_limit", e.ContainerCPU.Validate()},
		{"container_memory_limit_bytes", e.ContainerMemory.Validate()},
		{"filesystem", e.Filesystem.Validate()},
		{"power_mode", e.PowerMode.Validate()},
		{"dirty_worktree", e.DirtyWorktree.Validate()},
	}
	for _, field := range fields {
		if field.err != nil {
			return fmt.Errorf("environment %s: %w", field.name, field.err)
		}
	}
	return nil
}
