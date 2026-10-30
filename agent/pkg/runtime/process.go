// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

const maxReapedExits = 1024

var (
	managedPidsMu sync.RWMutex
	managedPids   = make(map[int]bool)

	reapedExitsMu   sync.Mutex
	reapedExits     = make(map[int]int)
	reapedExitsRing = make([]int, 0, maxReapedExits)
)

// RegisterChildPID registers an active child process PID spawned by os/exec.
func RegisterChildPID(pid int) {
	if pid <= 0 {
		return
	}
	managedPidsMu.Lock()
	defer managedPidsMu.Unlock()
	managedPids[pid] = true
}

// UnregisterChildPID removes a child process PID once it has been waited on.
func UnregisterChildPID(pid int) {
	if pid <= 0 {
		return
	}
	managedPidsMu.Lock()
	delete(managedPids, pid)
	managedPidsMu.Unlock()

	reapedExitsMu.Lock()
	delete(reapedExits, pid)
	for i, p := range reapedExitsRing {
		if p == pid {
			reapedExitsRing = append(reapedExitsRing[:i], reapedExitsRing[i+1:]...)
			break
		}
	}
	reapedExitsMu.Unlock()
}

// IsManagedChildPID checks if a PID is currently managed by os/exec.
func IsManagedChildPID(pid int) bool {
	managedPidsMu.RLock()
	defer managedPidsMu.RUnlock()
	return managedPids[pid]
}

// RecordReapedExit records the exit code of a child reaped by the zombie reaper.
// It records exits unconditionally in a bounded ring buffer map so fast-exiting
// children reaped by PID 1 retain their exit status before RegisterChildPID completes.
func RecordReapedExit(pid int, exitCode int) {
	if pid <= 0 {
		return
	}
	reapedExitsMu.Lock()
	defer reapedExitsMu.Unlock()
	if _, exists := reapedExits[pid]; !exists {
		if len(reapedExitsRing) >= maxReapedExits {
			oldest := reapedExitsRing[0]
			reapedExitsRing = reapedExitsRing[1:]
			delete(reapedExits, oldest)
		}
		reapedExitsRing = append(reapedExitsRing, pid)
	}
	reapedExits[pid] = exitCode
}

// PopReapedExit retrieves and removes the recorded exit code for a reaped child.
func PopReapedExit(pid int) (int, bool) {
	reapedExitsMu.Lock()
	defer reapedExitsMu.Unlock()
	code, ok := reapedExits[pid]
	if ok {
		delete(reapedExits, pid)
		for i, p := range reapedExitsRing {
			if p == pid {
				reapedExitsRing = append(reapedExitsRing[:i], reapedExitsRing[i+1:]...)
				break
			}
		}
	}
	return code, ok
}

// WaitManagedCmd waits on an exec.Cmd, translating ECHILD errors if the process
// was already reaped by the zombie reaper.
func WaitManagedCmd(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("nil process")
	}
	pid := cmd.Process.Pid
	err := cmd.Wait()
	if err == nil {
		return nil
	}

	if errors.Is(err, syscall.ECHILD) || strings.Contains(err.Error(), "no child processes") {
		if code, ok := PopReapedExit(pid); ok {
			if code == 0 {
				return nil
			}
			return fmt.Errorf("exit status %d", code)
		}
	}
	return err
}
