// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	MaxBulkStringLength = 64 * 1024 * 1024 // 64MB
	MaxArrayCount       = 65536
	MaxDepth            = 32
	MaxLineLength       = 64 * 1024 // 64KB
)

// Reader parses RESP streams
type Reader struct {
	r *bufio.Reader
}

// NewReader creates a new RESP stream reader
func NewReader(rd io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(rd)}
}

// ReadValue reads the next RESP Value from the stream
func (r *Reader) ReadValue() (Value, error) {
	return r.readValueWithDepth(0)
}

func (r *Reader) readValueWithDepth(depth int) (Value, error) {
	if depth > MaxDepth {
		return Value{}, fmt.Errorf("%w: recursion depth exceeded", ErrProtocol)
	}

	line, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	if len(line) == 0 {
		return Value{}, fmt.Errorf("%w: empty line in RESP stream", ErrProtocol)
	}

	typ := line[0]
	payload := string(line[1:])

	switch typ {
	case TypeSimpleString:
		return Value{Type: TypeSimpleString, Str: payload}, nil

	case TypeError:
		return Value{Type: TypeError, Str: payload}, nil

	case TypeInteger:
		num, err := strconv.ParseInt(payload, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("%w: invalid integer %q", ErrProtocol, payload)
		}
		return Value{Type: TypeInteger, Num: num}, nil

	case TypeBulkString:
		length, err := strconv.Atoi(payload)
		if err != nil {
			return Value{}, fmt.Errorf("%w: invalid bulk string length %q", ErrProtocol, payload)
		}
		if length < -1 {
			return Value{}, fmt.Errorf("%w: negative bulk string length %d", ErrProtocol, length)
		}
		if length > MaxBulkStringLength {
			return Value{}, fmt.Errorf("%w: bulk string length %d exceeds max %d", ErrProtocol, length, MaxBulkStringLength)
		}
		if length == -1 {
			return Value{Type: TypeBulkString, IsNull: true}, nil
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(r.r, buf); err != nil {
			return Value{}, err
		}
		if buf[length] != '\r' || buf[length+1] != '\n' {
			return Value{}, fmt.Errorf("%w: invalid bulk string terminating CRLF", ErrProtocol)
		}
		return Value{Type: TypeBulkString, Str: string(buf[:length])}, nil

	case TypeArray, TypePush, TypeSet:
		count, err := strconv.Atoi(payload)
		if err != nil {
			return Value{}, fmt.Errorf("%w: invalid array count %q", ErrProtocol, payload)
		}
		if count < -1 {
			return Value{}, fmt.Errorf("%w: negative array count %d", ErrProtocol, count)
		}
		if count > MaxArrayCount {
			return Value{}, fmt.Errorf("%w: array count %d exceeds max %d", ErrProtocol, count, MaxArrayCount)
		}
		if count == -1 {
			return Value{Type: typ, IsNull: true}, nil
		}
		arr := make([]Value, count)
		for i := 0; i < count; i++ {
			val, err := r.readValueWithDepth(depth + 1)
			if err != nil {
				return Value{}, err
			}
			arr[i] = val
		}
		return Value{Type: typ, Array: arr}, nil

	case TypeMap:
		count, err := strconv.Atoi(payload)
		if err != nil {
			return Value{}, fmt.Errorf("%w: invalid map count %q", ErrProtocol, payload)
		}
		if count < -1 {
			return Value{}, fmt.Errorf("%w: negative map count %d", ErrProtocol, count)
		}
		if count > MaxArrayCount {
			return Value{}, fmt.Errorf("%w: map count %d exceeds max %d", ErrProtocol, count, MaxArrayCount)
		}
		if count == -1 {
			return Value{Type: TypeMap, IsNull: true}, nil
		}
		m := make(map[string]Value, count)
		for i := 0; i < count; i++ {
			k, err := r.readValueWithDepth(depth + 1)
			if err != nil {
				return Value{}, err
			}
			v, err := r.readValueWithDepth(depth + 1)
			if err != nil {
				return Value{}, err
			}
			m[k.String()] = v
		}
		return Value{Type: TypeMap, Map: m}, nil

	case TypeNull:
		return Value{Type: TypeNull, IsNull: true}, nil

	case TypeBoolean:
		if payload == "t" {
			return Value{Type: TypeBoolean, Boolean: true}, nil
		}
		return Value{Type: TypeBoolean, Boolean: false}, nil

	default:
		return Value{}, fmt.Errorf("%w: unknown RESP type prefix %c", ErrProtocol, typ)
	}
}

func (r *Reader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > MaxLineLength {
			return nil, fmt.Errorf("%w: line length exceeds max %d", ErrProtocol, MaxLineLength)
		}
		if len(line) >= 2 && line[len(line)-2] == '\r' && line[len(line)-1] == '\n' {
			return line[:len(line)-2], nil
		}
	}
}

// ReadError converts a TypeError Value to an error if present
func ReadError(v Value) error {
	if v.Type == TypeError {
		return errors.New(strings.TrimPrefix(v.Str, "ERR "))
	}
	return nil
}
