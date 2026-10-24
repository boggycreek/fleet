// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package resp

import (
	"bytes"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func TestRESPReaderSimpleTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Value
	}{
		{"SimpleString", "+OK\r\n", Value{Type: TypeSimpleString, Str: "OK"}},
		{"Error", "-ERR something went wrong\r\n", Value{Type: TypeError, Str: "ERR something went wrong"}},
		{"Integer", ":1000\r\n", Value{Type: TypeInteger, Num: 1000}},
		{"NegativeInteger", ":-42\r\n", Value{Type: TypeInteger, Num: -42}},
		{"BulkString", "$5\r\nhello\r\n", Value{Type: TypeBulkString, Str: "hello"}},
		{"EmptyBulkString", "$0\r\n\r\n", Value{Type: TypeBulkString, Str: ""}},
		{"NullBulkString", "$-1\r\n", Value{Type: TypeBulkString, IsNull: true}},
		{"NullArray", "*-1\r\n", Value{Type: TypeArray, IsNull: true}},
		{"NullMap", "%-1\r\n", Value{Type: TypeMap, IsNull: true}},
		{"BooleanTrue", "#t\r\n", Value{Type: TypeBoolean, Boolean: true}},
		{"BooleanFalse", "#f\r\n", Value{Type: TypeBoolean, Boolean: false}},
		{"NullType", "_\r\n", Value{Type: TypeNull, IsNull: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(bytes.NewBufferString(tc.input))
			val, err := r.ReadValue()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val.Type != tc.expected.Type || val.Str != tc.expected.Str || val.Num != tc.expected.Num || val.IsNull != tc.expected.IsNull || val.Boolean != tc.expected.Boolean {
				t.Errorf("got %+v, expected %+v", val, tc.expected)
			}
		})
	}
}

func TestRESPReaderArraysAndMaps(t *testing.T) {
	input := "*3\r\n$3\r\nfoo\r\n$3\r\nbar\r\n:123\r\n"
	r := NewReader(bytes.NewBufferString(input))
	val, err := r.ReadValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr, err := val.AsArray()
	if err != nil {
		t.Fatalf("AsArray error: %v", err)
	}
	if len(arr) != 3 {
		t.Fatalf("expected 3 items, got %d", len(arr))
	}
	if arr[0].String() != "foo" || arr[1].String() != "bar" || arr[2].Num != 123 {
		t.Errorf("unexpected array contents: %v", arr)
	}

	// Test RESP3 Map
	mapInput := "%2\r\n+key1\r\n$4\r\nval1\r\n+key2\r\n:999\r\n"
	rMap := NewReader(bytes.NewBufferString(mapInput))
	valMap, err := rMap.ReadValue()
	if err != nil {
		t.Fatalf("unexpected map error: %v", err)
	}
	m, err := valMap.AsMap()
	if err != nil {
		t.Fatalf("AsMap error: %v", err)
	}
	if len(m) != 2 || m["key1"].String() != "val1" || m["key2"].Num != 999 {
		t.Errorf("unexpected map contents: %v", m)
	}
}

func TestRESPReaderLimits(t *testing.T) {
	longLine := bytes.Repeat([]byte("a"), MaxLineLength+1)

	var deeplyNested bytes.Buffer
	for i := 0; i < 33; i++ {
		deeplyNested.WriteString("*1\r\n")
	}
	deeplyNested.WriteString("+OK\r\n")

	limitTests := []struct {
		name  string
		input string
	}{
		{"NegativeBulkStringLen", "$-2\r\n"},
		{"OversizedBulkString", "$67108865\r\n"},
		{"NegativeArrayCount", "*-2\r\n"},
		{"OversizedArray", "*65537\r\n"},
		{"NegativeMapCount", "%-2\r\n"},
		{"OversizedMap", "%65537\r\n"},
		{"DeepRecursion", deeplyNested.String()},
		{"LineTooLong", string(longLine) + "\r\n"},
	}

	for _, tc := range limitTests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(bytes.NewBufferString(tc.input))
			_, err := r.ReadValue()
			if err == nil {
				t.Errorf("expected ErrProtocol for %s, got nil", tc.name)
			} else if !errors.Is(err, ErrProtocol) {
				t.Errorf("expected ErrProtocol for %s, got %v", tc.name, err)
			}
		})
	}
}

func TestRESPReaderErrors(t *testing.T) {
	errorInputs := []struct {
		name  string
		input string
	}{
		{"EmptyLine", "\r\n"},
		{"UnknownPrefix", "?unknown\r\n"},
		{"InvalidInt", ":notanumber\r\n"},
		{"InvalidBulkLen", "$invalid\r\n"},
		{"InvalidArrayCount", "*invalid\r\n"},
		{"InvalidMapCount", "%invalid\r\n"},
		{"TruncatedBulk", "$10\r\nshort\r\n"},
	}

	for _, tc := range errorInputs {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(bytes.NewBufferString(tc.input))
			_, err := r.ReadValue()
			if err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestValueStringFormatting(t *testing.T) {
	tests := []struct {
		val      Value
		expected string
	}{
		{Value{Type: TypeSimpleString, Str: "OK"}, "OK"},
		{Value{Type: TypeSimpleString, IsNull: true}, "<nil>"},
		{Value{Type: TypeInteger, Num: 42}, "42"},
		{Value{Type: TypeBoolean, Boolean: true}, "true"},
		{Value{Type: TypeBoolean, Boolean: false}, "false"},
		{Value{Type: TypeArray, IsNull: true}, "<nil array>"},
		{Value{Type: TypeArray, Array: []Value{{Type: TypeSimpleString, Str: "item"}}}, "[item]"},
		{Value{Type: TypeMap, Map: map[string]Value{"a": {Type: TypeSimpleString, Str: "1"}}}, "map[a:1]"},
		{Value{Type: TypeNull, IsNull: true}, "<nil>"},
		{Value{Type: 'X', Str: "fallback"}, "fallback"},
	}

	for _, tc := range tests {
		if tc.val.String() != tc.expected {
			t.Errorf("got %q, expected %q", tc.val.String(), tc.expected)
		}
	}
}

func TestValueConversions(t *testing.T) {
	// String conversions
	strVal := Value{Type: TypeBulkString, Str: "hello"}
	s, err := strVal.AsString()
	if err != nil || s != "hello" {
		t.Errorf("expected hello, got %s (err: %v)", s, err)
	}

	intAsStr := Value{Type: TypeInteger, Num: 99}
	s2, err := intAsStr.AsString()
	if err != nil || s2 != "99" {
		t.Errorf("expected '99', got %s", s2)
	}

	arrAsStr := Value{Type: TypeArray}
	if _, err := arrAsStr.AsString(); err == nil {
		t.Errorf("expected error converting array to string")
	}

	// Int conversions
	intVal := Value{Type: TypeInteger, Num: 42}
	n, err := intVal.AsInt()
	if err != nil || n != 42 {
		t.Errorf("expected 42, got %d (err: %v)", n, err)
	}

	numStrVal := Value{Type: TypeBulkString, Str: "100"}
	n2, err := numStrVal.AsInt()
	if err != nil || n2 != 100 {
		t.Errorf("expected 100, got %d (err: %v)", n2, err)
	}

	invalidNumStr := Value{Type: TypeBulkString, Str: "not-a-number"}
	if _, err := invalidNumStr.AsInt(); err == nil {
		t.Errorf("expected error converting non-numeric string to int")
	}

	arrAsInt := Value{Type: TypeArray}
	if _, err := arrAsInt.AsInt(); err == nil {
		t.Errorf("expected error converting array to int")
	}

	// Array conversions
	notArr := Value{Type: TypeInteger, Num: 10}
	if _, err := notArr.AsArray(); err == nil {
		t.Errorf("expected error converting int to array")
	}

	// Map conversions
	kvArray := Value{
		Type: TypeArray,
		Array: []Value{
			{Type: TypeBulkString, Str: "k1"},
			{Type: TypeBulkString, Str: "v1"},
			{Type: TypeBulkString, Str: "k2"},
			{Type: TypeInteger, Num: 50},
		},
	}
	kvMap, err := kvArray.AsMap()
	if err != nil {
		t.Fatalf("kvArray AsMap error: %v", err)
	}
	if kvMap["k1"].String() != "v1" || kvMap["k2"].Num != 50 {
		t.Errorf("unexpected kvMap: %v", kvMap)
	}

	oddArray := Value{
		Type: TypeArray,
		Array: []Value{
			{Type: TypeBulkString, Str: "k1"},
		},
	}
	if _, err := oddArray.AsMap(); err == nil {
		t.Errorf("expected error converting odd-length array to map")
	}

	notMapOrArr := Value{Type: TypeInteger, Num: 5}
	if _, err := notMapOrArr.AsMap(); err == nil {
		t.Errorf("expected error converting int to map")
	}

	// Null errors
	nullVal := Value{IsNull: true}
	if _, err := nullVal.AsString(); !errors.Is(err, ErrNil) {
		t.Errorf("expected ErrNil, got %v", err)
	}
	if _, err := nullVal.AsInt(); !errors.Is(err, ErrNil) {
		t.Errorf("expected ErrNil, got %v", err)
	}
	if _, err := nullVal.AsArray(); !errors.Is(err, ErrNil) {
		t.Errorf("expected ErrNil, got %v", err)
	}
	if _, err := nullVal.AsMap(); !errors.Is(err, ErrNil) {
		t.Errorf("expected ErrNil, got %v", err)
	}
}

func TestRESPWriter(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewWriter(buf)

	// Empty command test
	if err := w.WriteCommand(); err != nil {
		t.Errorf("unexpected error on empty command: %v", err)
	}

	err := w.WriteCommand("SET", "mykey", "myval")
	if err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	expected := "*3\r\n$3\r\nSET\r\n$5\r\nmykey\r\n$5\r\nmyval\r\n"
	if buf.String() != expected {
		t.Errorf("got %q, expected %q", buf.String(), expected)
	}

	// Test WriteValue for multiple types
	buf.Reset()
	values := []Value{
		{Type: TypeSimpleString, Str: "PONG"},
		{Type: TypeError, Str: "ERR syntax"},
		{Type: TypeInteger, Num: 777},
		{Type: TypeBulkString, Str: "payload"},
		{Type: TypeBoolean, Boolean: true},
		{Type: TypeBoolean, Boolean: false},
		{Type: TypeNull, IsNull: true},
		{Type: TypeBulkString, IsNull: true},
		{Type: 'Z', Str: "fallback_val"},
		{Type: TypeArray, Array: []Value{{Type: TypeSimpleString, Str: "A"}, {Type: TypeSimpleString, Str: "B"}}},
	}

	for _, v := range values {
		if err := w.WriteValue(v); err != nil {
			t.Fatalf("WriteValue error: %v", err)
		}
	}

	// Read them back using Reader to verify complete roundtrip
	r := NewReader(buf)
	for i, v := range values {
		readVal, err := r.ReadValue()
		if err != nil {
			t.Fatalf("failed reading roundtrip value #%d: %v", i, err)
		}
		if v.IsNull {
			if !readVal.IsNull {
				t.Errorf("#%d: expected null value, got %+v", i, readVal)
			}
			continue
		}
		if v.Type == TypeArray {
			if len(readVal.Array) != len(v.Array) {
				t.Errorf("#%d: array length mismatch: got %d, expected %d", i, len(readVal.Array), len(v.Array))
			}
			continue
		}
		if v.String() != readVal.String() {
			t.Errorf("#%d: mismatch string: got %s, expected %s", i, readVal.String(), v.String())
		}
	}
}

func TestEncodeCommand(t *testing.T) {
	encoded := EncodeCommand("XADD", "mystream", "*", "field", "value")
	expected := "*5\r\n$4\r\nXADD\r\n$8\r\nmystream\r\n$1\r\n*\r\n$5\r\nfield\r\n$5\r\nvalue\r\n"
	if !reflect.DeepEqual(encoded, []byte(expected)) {
		t.Errorf("got %q, expected %q", string(encoded), expected)
	}
}

func TestReadErrorHelper(t *testing.T) {
	errVal := Value{Type: TypeError, Str: "ERR invalid password"}
	err := ReadError(errVal)
	if err == nil || err.Error() != "invalid password" {
		t.Errorf("expected 'invalid password', got %v", err)
	}

	okVal := Value{Type: TypeSimpleString, Str: "OK"}
	if err := ReadError(okVal); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func BenchmarkEncodeCommand(b *testing.B) {
	args := []string{"SET", "mykey", "myvalue", "EX", "60"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeCommand(args...)
	}
}

func BenchmarkEncodeCommandLarge(b *testing.B) {
	var args []string
	for i := 0; i < 50; i++ {
		args = append(args, "arg"+strconv.Itoa(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeCommand(args...)
	}
}

func BenchmarkEncodeCommandLargePayload(b *testing.B) {
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = 'a'
	}
	payloadStr := string(payload)
	args := []string{"SET", "mykey", payloadStr, "EX", "60"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeCommand(args...)
	}
}
