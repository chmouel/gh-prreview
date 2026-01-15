package diffhunk

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Pre-compiled regexes for diff parsing (avoids recompilation on each call)
var (
	diffHeaderRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)
	hunkSplitRe  = regexp.MustCompile(`(?m)^@@[^@]+@@`)
)

// DiffChangeType represents the type of change in a diff line
type DiffChangeType int

const (
	Context DiffChangeType = iota
	Add
	Delete
	Control
)

// DiffLine represents a single line in a diff
type DiffLine struct {
	Type           DiffChangeType
	OldLineNumber  int    // 1-based, -1 if not applicable
	NewLineNumber  int    // 1-based, -1 if not applicable
	Text           string // Line content without the prefix (+, -, or space)
	PositionInHunk int    // Position within the hunk (0-based)
}

// DiffHunk represents a hunk in a unified diff
type DiffHunk struct {
	OldStart int         // Starting line in old file (1-based)
	OldLines int         // Number of lines in old file
	NewStart int         // Starting line in new file (1-based)
	NewLines int         // Number of lines in new file
	Lines    []*DiffLine // The actual diff lines
}

// ParseDiffHunk parses a unified diff hunk into a structured format
// Expected format:
// @@ -oldStart,oldLines +newStart,newLines @@
// context line
// +added line
// -removed line
func ParseDiffHunk(hunk string) (*DiffHunk, error) {
	lines := strings.Split(hunk, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty diff hunk")
	}

	// Parse header line: @@ -oldStart,oldLines +newStart,newLines @@
	matches := diffHeaderRe.FindStringSubmatch(lines[0])
	if matches == nil {
		return nil, fmt.Errorf("invalid diff hunk header: %s", lines[0])
	}

	oldStart, _ := strconv.Atoi(matches[1])
	oldLines := 1
	if matches[2] != "" {
		oldLines, _ = strconv.Atoi(matches[2])
	}

	newStart, _ := strconv.Atoi(matches[3])
	newLines := 1
	if matches[4] != "" {
		newLines, _ = strconv.Atoi(matches[4])
	}

	dh := &DiffHunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
		Lines:    make([]*DiffLine, 0),
	}

	oldLine := oldStart
	newLine := newStart
	position := 0

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if len(line) == 0 {
			continue
		}

		var diffLine DiffLine
		diffLine.PositionInHunk = position
		position++

		switch line[0] {
		case ' ': // Context line
			diffLine.Type = Context
			diffLine.OldLineNumber = oldLine
			diffLine.NewLineNumber = newLine
			diffLine.Text = line[1:]
			oldLine++
			newLine++
		case '+': // Added line
			diffLine.Type = Add
			diffLine.OldLineNumber = -1
			diffLine.NewLineNumber = newLine
			diffLine.Text = line[1:]
			newLine++
		case '-': // Removed line
			diffLine.Type = Delete
			diffLine.OldLineNumber = oldLine
			diffLine.NewLineNumber = -1
			diffLine.Text = line[1:]
			oldLine++
		case '\\': // "\ No newline at end of file"
			diffLine.Type = Control
			diffLine.OldLineNumber = -1
			diffLine.NewLineNumber = -1
			diffLine.Text = line
		default:
			// Unknown line type, treat as context
			diffLine.Type = Context
			diffLine.Text = line
		}

		dh.Lines = append(dh.Lines, &diffLine)
	}

	return dh, nil
}

// ParsePatch parses a complete unified diff patch into multiple hunks
func ParsePatch(patch string) ([]*DiffHunk, error) {
	// Split by hunk headers
	indices := hunkSplitRe.FindAllStringIndex(patch, -1)

	if len(indices) == 0 {
		return nil, fmt.Errorf("no hunks found in patch")
	}

	hunks := make([]*DiffHunk, 0, len(indices))

	for i, idx := range indices {
		start := idx[0]
		var end int
		if i < len(indices)-1 {
			end = indices[i+1][0]
		} else {
			end = len(patch)
		}

		hunkStr := patch[start:end]
		hunk, err := ParseDiffHunk(hunkStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse hunk %d: %w", i, err)
		}
		hunks = append(hunks, hunk)
	}

	return hunks, nil
}

// GetDiffLineByPosition finds a diff line by its position in the overall diff
// diffLineNumber is 1-based (GitHub's position)
func GetDiffLineByPosition(hunks []*DiffHunk, diffLineNumber int) *DiffLine {
	currentPos := 1 // GitHub positions are 1-based

	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			if currentPos == diffLineNumber {
				return line
			}
			// Only count lines that appear in the diff (not control lines)
			if line.Type != Control {
				currentPos++
			}
		}
	}

	return nil
}

// GetModifiedContentFromDiffHunk applies a diff hunk to original content
func GetModifiedContentFromDiffHunk(originalContent string, patch string) (string, error) {
	hunks, err := ParsePatch(patch)
	if err != nil {
		return "", err
	}

	originalLines := strings.Split(originalContent, "\n")
	result := make([]string, 0, len(originalLines))

	currentOldLine := 1

	for _, hunk := range hunks {
		// Add lines before this hunk
		for currentOldLine < hunk.OldStart {
			if currentOldLine-1 < len(originalLines) {
				result = append(result, originalLines[currentOldLine-1])
			}
			currentOldLine++
		}

		// Process the hunk
		for _, line := range hunk.Lines {
			switch line.Type {
			case Context:
				result = append(result, line.Text)
				currentOldLine++
			case Add:
				result = append(result, line.Text)
			case Delete:
				currentOldLine++
			}
		}
	}

	// Add remaining lines
	for currentOldLine <= len(originalLines) {
		result = append(result, originalLines[currentOldLine-1])
		currentOldLine++
	}

	return strings.Join(result, "\n"), nil
}

// GetZeroBased converts 1-based line numbers to 0-based
// Special case: 0 stays 0 (used for empty files)
func GetZeroBased(line int) int {
	if line == 0 {
		return 0
	}
	return line - 1
}

// GetAddedLines extracts all added lines from a diff hunk
func GetAddedLines(hunk string) []string {
	lines := strings.Split(hunk, "\n")
	var added []string

	for i, line := range lines {
		if i == 0 {
			continue // Skip @@ header
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == '+' {
			added = append(added, line[1:])
		}
	}

	return added
}

// GetRemovedLines extracts all removed lines from a diff hunk
func GetRemovedLines(hunk string) []string {
	lines := strings.Split(hunk, "\n")
	var removed []string

	for i, line := range lines {
		if i == 0 {
			continue // Skip @@ header
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == '-' {
			removed = append(removed, line[1:])
		}
	}

	return removed
}
