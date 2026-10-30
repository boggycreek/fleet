// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrInvalidFrontmatter = errors.New("invalid or missing YAML frontmatter")

// ParseDocument parses a Markdown file containing YAML frontmatter into a MemoryItem.
func ParseDocument(content string) (*MemoryItem, error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil, ErrInvalidFrontmatter
	}

	startIdx := -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if trimmed == "---" {
			startIdx = i
			break
		}
		// First non-empty line is not '---'
		return nil, ErrInvalidFrontmatter
	}

	if startIdx == -1 {
		return nil, ErrInvalidFrontmatter
	}

	endIdx := -1
	for i := startIdx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}

	if endIdx == -1 {
		return nil, ErrInvalidFrontmatter
	}

	fmLines := lines[startIdx+1 : endIdx]
	bodyLines := lines[endIdx+1:]

	item := &MemoryItem{}
	var currentKey string
	var inTagsList bool

	for _, line := range fmLines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
			continue
		}

		if inTagsList && (strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(trimmedLine, "-")) {
			tagVal := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "-"))
			tagVal = unquote(tagVal)
			if tagVal != "" {
				item.Tags = append(item.Tags, tagVal)
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			val := strings.TrimSpace(parts[1])

			currentKey = key
			inTagsList = false

			switch key {
			case "id":
				item.ID = unquote(val)
			case "timestamp":
				item.Timestamp = unquote(val)
			case "pillar":
				item.Pillar = unquote(val)
			case "title":
				item.Title = unquote(val)
			case "tier1_abstract":
				item.Tier1Abstract = unquote(val)
			case "tier2_summary":
				item.Tier2Summary = unquote(val)
			case "tags":
				if val == "" {
					inTagsList = true
				} else {
					item.Tags = parseTagsValue(val)
				}
			}
		} else if currentKey != "" {
			// Continuation of previous multiline string value
			val := unquote(trimmedLine)
			switch currentKey {
			case "tier1_abstract":
				if item.Tier1Abstract != "" {
					item.Tier1Abstract += " " + val
				} else {
					item.Tier1Abstract = val
				}
			case "tier2_summary":
				if item.Tier2Summary != "" {
					item.Tier2Summary += " " + val
				} else {
					item.Tier2Summary = val
				}
			}
		}
	}

	body := strings.Join(bodyLines, "\n")
	item.Body = strings.TrimSpace(body)

	return item, nil
}

// FormatDocument serializes a MemoryItem into a Markdown document with YAML frontmatter.
func FormatDocument(item *MemoryItem) string {
	var sb strings.Builder
	sb.WriteString("---\n")

	if item.ID != "" {
		fmt.Fprintf(&sb, "id: %s\n", quoteIfNeeded(item.ID))
	}
	if item.Timestamp != "" {
		fmt.Fprintf(&sb, "timestamp: %s\n", quoteIfNeeded(item.Timestamp))
	}
	if item.Pillar != "" {
		fmt.Fprintf(&sb, "pillar: %s\n", quoteIfNeeded(item.Pillar))
	}
	if item.Title != "" {
		fmt.Fprintf(&sb, "title: %s\n", quoteIfNeeded(item.Title))
	}

	if len(item.Tags) > 0 {
		sb.WriteString("tags:\n")
		for _, tag := range item.Tags {
			fmt.Fprintf(&sb, "  - %s\n", quoteIfNeeded(tag))
		}
	} else {
		sb.WriteString("tags: []\n")
	}

	if item.Tier1Abstract != "" {
		fmt.Fprintf(&sb, "tier1_abstract: %s\n", quoteIfNeeded(item.Tier1Abstract))
	}
	if item.Tier2Summary != "" {
		fmt.Fprintf(&sb, "tier2_summary: %s\n", quoteIfNeeded(item.Tier2Summary))
	}

	sb.WriteString("---\n")

	if item.Body != "" {
		sb.WriteString("\n")
		sb.WriteString(item.Body)
		sb.WriteString("\n")
	}

	return sb.String()
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
		return s[1 : len(s)-1]
	}
	return s
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, ":\n#[]{}'\"") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func parseTagsValue(val string) []string {
	val = strings.TrimSpace(val)
	if val == "[]" || val == "" {
		return nil
	}

	if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
		inner := val[1 : len(val)-1]
		parts := strings.Split(inner, ",")
		var tags []string
		for _, p := range parts {
			tag := unquote(strings.TrimSpace(p))
			if tag != "" {
				tags = append(tags, tag)
			}
		}
		return tags
	}

	single := unquote(val)
	if single != "" {
		return []string{single}
	}
	return nil
}

// readLines returns non-empty line scanner.
func readLines(content string) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}
