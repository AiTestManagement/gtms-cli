package adapter

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// streamResult holds the output from streaming stdout parsing.
type streamResult struct {
	Summary    string   // non-file content (before first delimiter, after closing tag, or all stdout)
	SavedFiles []string // absolute paths of files written to outputDir
}

// parseStreamingOutput reads from reader line by line, detecting XML file tags
// (<gtms-file name="...">...</gtms-file>). Content between tags is written to
// outputDir/<filename> as each block completes. Content outside tags (before the first
// opening tag, between closing and opening tags, or after the last closing tag) becomes
// the Summary. Only one file block is in memory at a time. If outputDir is empty, all
// output is treated as summary (no file extraction).
func parseStreamingOutput(reader io.Reader, outputDir string, force bool) (*streamResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // max 10MB per line

	var summaryBuf strings.Builder
	var currentFile string
	var currentBuf strings.Builder
	var savedFiles []string
	inFile := false

	for scanner.Scan() {
		line := scanner.Text()

		if outputDir != "" {
			// Check for XML closing tag (must check BEFORE opening tags)
			if inFile && isXMLFileClose(line) {
				// Write current file block if we have a valid filename
				if currentFile != "" && currentBuf.Len() > 0 {
					path, err := writeFileBlock(outputDir, currentFile, currentBuf.String(), force)
					if err != nil {
						return nil, fmt.Errorf("writing file %s: %w", currentFile, err)
					}
					if path != "" {
						savedFiles = append(savedFiles, path)
					}
				}
				// Revert to summary mode — content after </gtms-file> goes to summaryBuf
				currentFile = ""
				currentBuf.Reset()
				inFile = false
				continue
			}

			// Check for XML opening tag
			if isXMLFileOpen(line) {
				filename := extractXMLFilename(line)

				// Sanitize filename: reject directory traversal attempts
				if !isSafeFilename(filename) {
					fmt.Fprintf(os.Stderr, "warning: skipping unsafe filename: %s\n", filename)
					// If we were in a file block, write it before skipping this delimiter
					if inFile && currentFile != "" && currentBuf.Len() > 0 {
						path, err := writeFileBlock(outputDir, currentFile, currentBuf.String(), force)
						if err != nil {
							return nil, fmt.Errorf("writing file %s: %w", currentFile, err)
						}
						if path != "" {
							savedFiles = append(savedFiles, path)
						}
					}
					// Discard the bad block — stay in file mode with empty filename
					// so subsequent lines are discarded until </gtms-file>
					currentFile = ""
					currentBuf.Reset()
					inFile = true
					continue
				}

				// Write previous file block if we were in one
				if inFile && currentFile != "" && currentBuf.Len() > 0 {
					path, err := writeFileBlock(outputDir, currentFile, currentBuf.String(), force)
					if err != nil {
						return nil, fmt.Errorf("writing file %s: %w", currentFile, err)
					}
					if path != "" {
						savedFiles = append(savedFiles, path)
					}
				}

				// Start new file block
				currentFile = filename
				currentBuf.Reset()
				inFile = true
				continue
			}
		}

		// Accumulate content
		if inFile {
			if currentBuf.Len() > 0 {
				currentBuf.WriteByte('\n')
			}
			currentBuf.WriteString(line)
		} else {
			if summaryBuf.Len() > 0 {
				summaryBuf.WriteByte('\n')
			}
			summaryBuf.WriteString(line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning stdout: %w", err)
	}

	// Write last file block if complete (skip if filename is empty — discarded block)
	if inFile && currentFile != "" && currentBuf.Len() > 0 {
		path, err := writeFileBlock(outputDir, currentFile, currentBuf.String(), force)
		if err != nil {
			return nil, fmt.Errorf("writing file %s: %w", currentFile, err)
		}
		if path != "" {
			savedFiles = append(savedFiles, path)
		}
	}

	return &streamResult{
		Summary:    strings.TrimSpace(summaryBuf.String()),
		SavedFiles: savedFiles,
	}, nil
}

// isXMLFileOpen checks if a line matches the <gtms-file name="..."> pattern on its own line.
func isXMLFileOpen(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "<gtms-file ") && strings.HasSuffix(trimmed, ">")
}

// isXMLFileClose checks if a line is a </gtms-file> closing tag on its own line.
func isXMLFileClose(line string) bool {
	return strings.TrimSpace(line) == "</gtms-file>"
}

// extractXMLFilename extracts the filename from a <gtms-file name="..."> opening tag.
// Uses simple string operations — no regex, no XML parser.
func extractXMLFilename(line string) string {
	trimmed := strings.TrimSpace(line)
	idx := strings.Index(trimmed, `name="`)
	if idx < 0 {
		return ""
	}
	start := idx + len(`name="`)
	end := strings.Index(trimmed[start:], `"`)
	if end < 0 {
		return ""
	}
	return trimmed[start : start+end]
}

// isSafeFilename rejects directory traversal and absolute paths but allows
// relative subdirectory paths (e.g. "subdir/file.bats"). Adapters can route
// output files into subdirectories via {output_subdir}.
func isSafeFilename(name string) bool {
	if name == "" {
		return false
	}
	// Block directory traversal
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return false
		}
	}
	// Block absolute paths (Unix or Windows)
	if filepath.IsAbs(name) || (len(name) >= 2 && name[1] == ':') {
		return false
	}
	// Block backslashes — require forward slashes for portability
	if strings.Contains(name, "\\") {
		return false
	}
	return true
}

// writeFileBlock writes content to outputDir/filename, creating the directory if needed.
// Returns the absolute path of the written file, or ("", nil) if the file already exists
// and force is false (duplicate guard — warns on stderr and skips to avoid overwriting
// existing content). When force is true, existing files are overwritten (BUG-031).
func writeFileBlock(outputDir, filename, content string, force bool) (string, error) {
	path := filepath.Join(outputDir, filename)

	// Duplicate guard: warn and skip if file already exists (ENH-042)
	// Bypassed when force is true — user explicitly requested overwrite (BUG-031)
	if !force {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "warning: skipping duplicate file: %s\n", filename)
			return "", nil
		}
	}

	// MkdirAll the parent directory — handles both the base outputDir
	// and any subdirectory within the filename (e.g. "widgets/tc-abc.bats").
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("creating output directory: %w", err)
	}

	// Add trailing newline to file content
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}

	return path, nil
}
