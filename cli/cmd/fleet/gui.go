// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func handleGUI(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "Launching Backplane GUI client...")
	return 0
}
