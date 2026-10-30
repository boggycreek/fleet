// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package resp

import (
	"bytes"
	"testing"
)

func FuzzRESPReader(f *testing.F) {
	// Seed corpus with valid RESP values, partial tokens, and edge cases
	f.Add([]byte("+OK\r\n"))
	f.Add([]byte("-ERR unknown command\r\n"))
	f.Add([]byte(":1000\r\n"))
	f.Add([]byte(":-42\r\n"))
	f.Add([]byte("$5\r\nhello\r\n"))
	f.Add([]byte("$0\r\n\r\n"))
	f.Add([]byte("$-1\r\n"))
	f.Add([]byte("*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"))
	f.Add([]byte("*0\r\n"))
	f.Add([]byte("*-1\r\n"))
	f.Add([]byte("%1\r\n+key\r\n+val\r\n"))
	f.Add([]byte("%-1\r\n"))
	f.Add([]byte("#t\r\n"))
	f.Add([]byte("#f\r\n"))
	f.Add([]byte("_\r\n"))
	f.Add([]byte("$100\r\ntruncated"))
	f.Add([]byte("*1000000000\r\n"))
	f.Add([]byte("invalid\r\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		// Robustness check: ReadValue must never panic on arbitrary binary input
		for {
			val, err := r.ReadValue()
			if err != nil {
				break
			}
			// Exercising formatting and conversions should also not panic
			_ = val.String()
			_, _ = val.AsString()
			_, _ = val.AsInt()
			_, _ = val.AsArray()
			_, _ = val.AsMap()
		}
	})
}
