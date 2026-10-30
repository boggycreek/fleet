// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package resp

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRESPReaderBulkStringCRLFValidation(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		errContains string
	}{
		{
			name:        "ValidBulkString",
			input:       "$5\r\nhello\r\n",
			expectError: false,
		},
		{
			name:        "CorruptCRLF_BothWrong",
			input:       "$5\r\nhelloXX",
			expectError: true,
			errContains: "invalid bulk string terminating CRLF",
		},
		{
			name:        "CorruptCRLF_FirstWrong",
			input:       "$5\r\nhello\n\n",
			expectError: true,
			errContains: "invalid bulk string terminating CRLF",
		},
		{
			name:        "CorruptCRLF_SecondWrong",
			input:       "$5\r\nhello\r\r",
			expectError: true,
			errContains: "invalid bulk string terminating CRLF",
		},
		{
			name:        "CorruptCRLF_NullBytes",
			input:       "$4\r\ntest\x00\x00",
			expectError: true,
			errContains: "invalid bulk string terminating CRLF",
		},
		{
			name:        "ValidBulkStringWithInteriorCRLF",
			input:       "$8\r\nfoo\r\nbar\r\n",
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(bytes.NewBufferString(tc.input))
			val, err := r.ReadValue()
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errContains)
				}
				if !errors.Is(err, ErrProtocol) {
					t.Errorf("expected ErrProtocol, got: %v", err)
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("expected error containing %q, got: %v", tc.errContains, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if val.Type != TypeBulkString {
					t.Errorf("expected TypeBulkString, got %v", val.Type)
				}
			}
		})
	}
}
