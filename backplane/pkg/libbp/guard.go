// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrForbiddenCommand  = errors.New("forbidden command")
	ErrDestructiveAction = errors.New("destructive action refused by client guard")
)

var safeCommands = map[string]bool{
	"PING":      true,
	"AUTH":      true,
	"XADD":      true,
	"XREAD":     true,
	"XREVRANGE": true,
	"XRANGE":    true,
	"INCR":      true,
	"GET":       true,
	"SET":       true,
	"HGET":      true,
	"HSET":      true,
	"HGETALL":   true,
	"SCAN":      true,
	"DEL":       true,
	"UNLINK":    true,
	"XTRIM":     true,
	"XDEL":      true,
	"HSETNX":    true,
	"SADD":      true,
	"SREM":      true,
	"SMEMBERS":  true,
	"SCARD":     true,
	"SISMEMBER": true,
}

var dangerousCommands = map[string]bool{
	"EXPIRE":    true,
	"PEXPIRE":   true,
	"EXPIREAT":  true,
	"PEXPIREAT": true,
	"RENAME":    true,
	"RENAMENX":  true,
	"MSET":      true,
	"MSETNX":    true,
	"HDEL":      true,
	"XSETID":    true,
	"EVAL":      true,
	"EVALSHA":   true,
	"FCALL":     true,
	"FCALL_RO":  true,
	"FLUSHALL":  true,
	"FLUSHDB":   true,
	"SHUTDOWN":  true,
	"DEBUG":     true,
	"CONFIG":    true,
	"KEYS":      true,
}

func isPeer(owner, callerID string) bool {
	caller := strings.ToLower(strings.TrimSpace(callerID))
	if caller == "" {
		return true
	}
	if caller == "admin" || caller == "operator" {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(owner), caller)
}

func isPeerInbox(key, callerID string) bool {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if !strings.HasSuffix(lowerKey, ":inbox") {
		return false
	}
	owner := strings.TrimSuffix(lowerKey, ":inbox")
	return isPeer(owner, callerID)
}

func isPeerOutbox(key, callerID string) bool {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if !strings.HasSuffix(lowerKey, ":out") || strings.HasPrefix(lowerKey, "ensemble:") {
		return false
	}
	owner := strings.TrimSuffix(lowerKey, ":out")
	return isPeer(owner, callerID)
}

func isPeerIdentity(key, callerID string) bool {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if !strings.HasPrefix(lowerKey, "identity:") {
		return false
	}
	owner := strings.TrimPrefix(lowerKey, "identity:")
	return isPeer(owner, callerID)
}

func isPeerStreamOrInbox(key, callerID string) bool {
	return isPeerInbox(key, callerID) || isPeerOutbox(key, callerID) || isPeerIdentity(key, callerID)
}

// AssertAllowed checks that a command and its arguments obey backplane safety rules
func AssertAllowed(callerID string, cmd string, args ...string) error {
	upperCmd := strings.ToUpper(strings.TrimSpace(cmd))
	if upperCmd == "" {
		return nil
	}
	if dangerousCommands[upperCmd] {
		return fmt.Errorf("%w: command %s is disabled in agent backplane", ErrForbiddenCommand, upperCmd)
	}

	// Administrative commands allowed only for admin or operator
	if upperCmd == "ACL" {
		caller := strings.ToLower(strings.TrimSpace(callerID))
		if caller == "admin" || caller == "operator" {
			return nil
		}
		return fmt.Errorf("%w: command ACL requires administrative credentials", ErrForbiddenCommand)
	}

	if !safeCommands[upperCmd] {
		return fmt.Errorf("%w: command %s is not permitted in agent backplane", ErrForbiddenCommand, upperCmd)
	}

	// Ensure GET poll-interval and SET poll-interval are allowed for any caller
	if (upperCmd == "GET" || upperCmd == "SET") && len(args) > 0 && strings.EqualFold(args[0], KeyPollInterval) {
		return nil
	}

	switch upperCmd {
	case "XADD":
		if len(args) < 1 {
			return fmt.Errorf("%w: XADD requires a target stream key", ErrForbiddenCommand)
		}
		targetKey := args[0]
		if isPeerOutbox(targetKey, callerID) {
			return fmt.Errorf("%w: writing to peer outbox %s is forbidden", ErrDestructiveAction, targetKey)
		}
		if isPeerIdentity(targetKey, callerID) {
			return fmt.Errorf("%w: writing to peer identity %s is forbidden", ErrDestructiveAction, targetKey)
		}
		if isPeerInbox(targetKey, callerID) {
			for _, arg := range args[1:] {
				upperArg := strings.ToUpper(strings.TrimSpace(arg))
				if upperArg == "MAXLEN" || upperArg == "MINID" || upperArg == "LIMIT" {
					return fmt.Errorf("%w: XADD with %s is forbidden on peer inbox %s", ErrDestructiveAction, upperArg, targetKey)
				}
			}
		}

	case "XTRIM", "XDEL", "DEL", "UNLINK":
		for _, arg := range args {
			if isPeerStreamOrInbox(arg, callerID) {
				return fmt.Errorf("%w: %s on peer stream or inbox %s is forbidden", ErrDestructiveAction, upperCmd, arg)
			}
		}

	case "SET", "HSET", "HSETNX":
		if len(args) > 0 && isPeerStreamOrInbox(args[0], callerID) {
			return fmt.Errorf("%w: %s on peer stream or identity %s is forbidden", ErrDestructiveAction, upperCmd, args[0])
		}
	}

	return nil
}
