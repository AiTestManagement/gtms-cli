// Package result manages the lifecycle of result contracts.
// Result contracts are YAML files in .gtms/results/ that track adapter invocation outcomes.
package result

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ResultContract represents the result of an adapter invocation.
type ResultContract struct {
	Task      string `yaml:"task"`
	Command   string `yaml:"command"`
	Target    string `yaml:"target"`
	Adapter   string `yaml:"adapter"`
	Mode      string `yaml:"mode"`
	Created   string `yaml:"created"`
	Status    string `yaml:"status"`               // pending, complete, error
	Artefact  string `yaml:"artefact,omitempty"`
	Attempts  int    `yaml:"attempts,omitempty"`
	Summary   string `yaml:"summary,omitempty"`
	Log       string `yaml:"log,omitempty"`
	Completed string `yaml:"completed,omitempty"`
}

// Create writes a new result contract to .gtms/results/{task-id}.result.yaml.
// Returns the filepath of the created file.
func Create(projectRoot string, rc *ResultContract) (string, error) {
	if rc.Task == "" {
		return "", fmt.Errorf("result contract task ID is required")
	}
	if rc.Status == "" {
		rc.Status = "pending"
	}

	// Ensure the results directory exists
	dir := filepath.Join(projectRoot, ".gtms", "results")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating results directory: %w", err)
	}

	filename := fmt.Sprintf("%s.result.yaml", rc.Task)
	path := filepath.Join(dir, filename)

	data, err := yaml.Marshal(rc)
	if err != nil {
		return "", fmt.Errorf("marshalling result contract: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("writing result contract: %w", err)
	}

	return path, nil
}

// Read parses a result contract from the given path.
func Read(path string) (*ResultContract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading result contract: %w", err)
	}

	var rc ResultContract
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("parsing result contract: %w", err)
	}

	return &rc, nil
}

// Update reads a result contract, applies updates, and writes it back.
// The updates map keys correspond to YAML field names.
func Update(path string, updates map[string]interface{}) error {
	// Read the existing file as a generic map to preserve all fields
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading result contract for update: %w", err)
	}

	var existing map[string]interface{}
	if err := yaml.Unmarshal(data, &existing); err != nil {
		return fmt.Errorf("parsing result contract for update: %w", err)
	}

	// Apply updates
	for k, v := range updates {
		existing[k] = v
	}

	// Write back
	out, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshalling updated result contract: %w", err)
	}

	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("writing updated result contract: %w", err)
	}

	return nil
}

// ResultPath returns the expected path for a result contract given a project root and task ID.
func ResultPath(projectRoot, taskID string) string {
	return filepath.Join(projectRoot, ".gtms", "results", fmt.Sprintf("%s.result.yaml", taskID))
}
