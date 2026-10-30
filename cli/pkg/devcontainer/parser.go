// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package devcontainer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// ParseDevContainer parses raw JSON or JSONC bytes into a DevContainerSpec.
func ParseDevContainer(data []byte) (*DevContainerSpec, error) {
	cleaned := CleanJSONC(data)
	var spec DevContainerSpec
	if err := json.Unmarshal(cleaned, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse devcontainer spec: %w", err)
	}
	return &spec, nil
}

// ParseDevContainerFile reads a file and parses its JSON or JSONC content into a DevContainerSpec.
func ParseDevContainerFile(path string) (*DevContainerSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read devcontainer file: %w", err)
	}
	return ParseDevContainer(data)
}

// CleanJSONC strips single-line comments (//...), multi-line comments (/*...*/),
// and trailing commas from JSON with comments (JSONC) data.
func CleanJSONC(input []byte) []byte {
	// Pass 1: Remove comments
	var buf bytes.Buffer
	inString := false
	escaped := false
	inSingleComment := false
	inMultiComment := false

	for i := 0; i < len(input); i++ {
		b := input[i]

		if inSingleComment {
			if b == '\n' {
				inSingleComment = false
				buf.WriteByte(b)
			}
			continue
		}

		if inMultiComment {
			if b == '*' && i+1 < len(input) && input[i+1] == '/' {
				inMultiComment = false
				i++
			} else if b == '\n' {
				buf.WriteByte(b)
			}
			continue
		}

		if inString {
			buf.WriteByte(b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// Not in string or comment
		if b == '"' {
			inString = true
			buf.WriteByte(b)
		} else if b == '/' && i+1 < len(input) && input[i+1] == '/' {
			inSingleComment = true
			i++
		} else if b == '/' && i+1 < len(input) && input[i+1] == '*' {
			inMultiComment = true
			i++
		} else {
			buf.WriteByte(b)
		}
	}

	commentStripped := buf.Bytes()

	// Pass 2: Remove trailing commas
	var finalBuf bytes.Buffer
	inString = false
	escaped = false

	for i := 0; i < len(commentStripped); i++ {
		b := commentStripped[i]

		if inString {
			finalBuf.WriteByte(b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		if b == '"' {
			inString = true
			finalBuf.WriteByte(b)
			continue
		}

		if b == ',' {
			// Look ahead to see if the next non-whitespace character is ] or }
			isTrailing := false
			for j := i + 1; j < len(commentStripped); j++ {
				next := commentStripped[j]
				if next == ' ' || next == '\t' || next == '\n' || next == '\r' {
					continue
				}
				if next == ']' || next == '}' {
					isTrailing = true
				}
				break
			}
			if isTrailing {
				// Skip this comma
				continue
			}
		}

		finalBuf.WriteByte(b)
	}

	return finalBuf.Bytes()
}
