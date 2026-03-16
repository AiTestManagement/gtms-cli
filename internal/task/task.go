// Package task manages task file CRUD and state machine transitions.
// Task files are markdown with YAML frontmatter stored under test-tasks/{status}/.
package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"
)

// TaskFile represents a GTMS task file with YAML frontmatter.
type TaskFile struct {
	ID        string `yaml:"id"`
	Type      string `yaml:"type"`                // create, automate, execute
	Target    string `yaml:"target"`              // requirement ID or test case ID
	Adapter   string `yaml:"adapter"`
	Status    string `yaml:"status"`              // pending, in-progress, in-review, complete, failed
	Created   string `yaml:"created"`             // ISO 8601
	Branch    string `yaml:"branch"`
	Error     string `yaml:"error,omitempty"`
	Reference string `yaml:"reference,omitempty"`  // create: --reference flag value; automate: test case source path
	Framework string `yaml:"framework,omitempty"` // automate only
}

// ValidStatuses lists all valid task status values.
var ValidStatuses = []string{"pending", "in-progress", "in-review", "complete", "failed"}

// Create writes a new task file to test-tasks/{status}/ with YAML frontmatter and body content.
// Returns the filepath of the created file.
func Create(projectRoot string, tf *TaskFile, body string) (string, error) {
	if tf.ID == "" {
		return "", fmt.Errorf("task ID is required")
	}
	if tf.Type == "" {
		return "", fmt.Errorf("task type is required")
	}
	if tf.Target == "" {
		return "", fmt.Errorf("task target is required")
	}
	if tf.Status == "" {
		tf.Status = "pending"
	}
	if tf.Created == "" {
		tf.Created = time.Now().UTC().Format(time.RFC3339)
	}

	// Ensure the status directory exists
	dir := filepath.Join(projectRoot, "test-tasks", tf.Status)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating task directory: %w", err)
	}

	// Build filename: task-{uuid}-{command}-{target}.md
	filename := tf.Filename()
	path := filepath.Join(dir, filename)

	// Marshal frontmatter
	fm, err := yaml.Marshal(tf)
	if err != nil {
		return "", fmt.Errorf("marshalling task frontmatter: %w", err)
	}

	// Write file with frontmatter and body
	content := fmt.Sprintf("---\n%s---\n%s", string(fm), body)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing task file: %w", err)
	}

	return path, nil
}

// Read parses a task file from the given path, extracting YAML frontmatter.
func Read(path string) (*TaskFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening task file: %w", err)
	}
	defer f.Close()

	var tf TaskFile
	_, err = frontmatter.Parse(f, &tf)
	if err != nil {
		return nil, fmt.Errorf("parsing task frontmatter: %w", err)
	}

	return &tf, nil
}

// Move physically moves a task file from its current status directory to a new one.
// It also updates the Status field in the frontmatter.
func Move(projectRoot string, tf *TaskFile, newStatus string) error {
	if !isValidStatus(newStatus) {
		return fmt.Errorf("invalid status: %s", newStatus)
	}

	oldStatus := tf.Status
	if oldStatus == newStatus {
		return nil // no-op
	}

	// Build old and new paths
	filename := tf.Filename()
	oldPath := filepath.Join(projectRoot, "test-tasks", oldStatus, filename)
	newDir := filepath.Join(projectRoot, "test-tasks", newStatus)
	newPath := filepath.Join(newDir, filename)

	// Ensure new directory exists
	if err := os.MkdirAll(newDir, 0755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}

	// Read the current file to get the body
	oldFile, err := os.Open(oldPath)
	if err != nil {
		return fmt.Errorf("opening task file for move: %w", err)
	}

	var oldTF TaskFile
	body, err := frontmatter.Parse(oldFile, &oldTF)
	oldFile.Close()
	if err != nil {
		return fmt.Errorf("parsing task file for move: %w", err)
	}

	// Update status
	tf.Status = newStatus

	// Re-write with updated frontmatter
	fm, err := yaml.Marshal(tf)
	if err != nil {
		return fmt.Errorf("marshalling updated frontmatter: %w", err)
	}

	content := fmt.Sprintf("---\n%s---\n%s", string(fm), string(body))
	if err := os.WriteFile(newPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing moved task file: %w", err)
	}

	// Remove old file
	if err := os.Remove(oldPath); err != nil {
		return fmt.Errorf("removing old task file: %w", err)
	}

	return nil
}

// List returns all task files matching the given statuses.
// If no statuses are specified, all statuses are scanned.
func List(projectRoot string, statuses ...string) ([]*TaskFile, error) {
	if len(statuses) == 0 {
		statuses = ValidStatuses
	}

	var results []*TaskFile

	for _, status := range statuses {
		dir := filepath.Join(projectRoot, "test-tasks", status)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			tf, err := Read(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
				continue
			}
			// Use directory name as canonical status
			tf.Status = status
			results = append(results, tf)
		}
	}

	return results, nil
}

// FindByTarget finds a task by type and target across the given statuses.
// If no statuses are specified, checks pending and in-progress.
func FindByTarget(projectRoot, taskType, target string, statuses ...string) (*TaskFile, error) {
	if len(statuses) == 0 {
		statuses = []string{"pending", "in-progress"}
	}

	tasks, err := List(projectRoot, statuses...)
	if err != nil {
		return nil, err
	}

	for _, tf := range tasks {
		if tf.Type == taskType && tf.Target == target {
			return tf, nil
		}
	}

	return nil, nil // not found is not an error
}

// Filename returns the canonical filename for this task file.
// It sanitizes the target to prevent path traversal and double extensions.
func (tf *TaskFile) Filename() string {
	return fmt.Sprintf("%s-%s-%s.md", tf.ID, tf.Type, sanitizeTarget(tf.Target))
}

// sanitizeTarget cleans a target string for safe use in filenames.
// Replaces path separators with hyphens (preserving directory components for uniqueness),
// and removes .md extension to prevent double .md.md.
func sanitizeTarget(target string) string {
	// Normalize backslashes to forward slashes
	target = strings.ReplaceAll(target, "\\", "/")

	// Strip .md extension to prevent double .md.md
	target = strings.TrimSuffix(target, ".md")

	// Replace path separators with hyphens (keeps directory components for uniqueness)
	target = strings.ReplaceAll(target, "/", "-")

	return target
}

// isValidStatus checks whether a status string is valid.
func isValidStatus(status string) bool {
	for _, s := range ValidStatuses {
		if s == status {
			return true
		}
	}
	return false
}
