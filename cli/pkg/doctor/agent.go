// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

// resolveAgentModelURL extracts the inference gateway URL from the agent configuration
// or inspects the running container environment for MODEL_URL / OPENAI_BASE_URL.
func resolveAgentModelURL(ctx context.Context, cfg *config.AgentConfig) string {
	if cfg == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(cfg.ModelURL); trimmed != "" {
		return trimmed
	}
	if cfg.ContainerName != "" && !cfg.IsNoContainer() && !cfg.IsBaremetal() {
		cmd := runtime.CommandContext(ctx, "podman", "container", "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cfg.ContainerName)
		if out, err := cmd.Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "MODEL_URL=") {
					val := strings.TrimSpace(strings.TrimPrefix(line, "MODEL_URL="))
					if val != "" {
						val = strings.ReplaceAll(val, "://llm-gateway", "://127.0.0.1")
						return val
					}
				}
				if strings.HasPrefix(line, "OPENAI_BASE_URL=") {
					val := strings.TrimSpace(strings.TrimPrefix(line, "OPENAI_BASE_URL="))
					if val != "" {
						val = strings.ReplaceAll(val, "://llm-gateway", "://127.0.0.1")
						return val
					}
				}
			}
		}
	}
	return ""
}

// checkModelGateway probes the configured model gateway for reachability, latency,
// detected engine, and available models.
func checkModelGateway(ctx context.Context, cfg *config.AgentConfig, report *DoctorReport) {
	modelURL := resolveAgentModelURL(ctx, cfg)
	if modelURL == "" {
		return
	}

	res, err := ProbeGateway(ctx, modelURL)
	if err == nil && res != nil && res.Reachable {
		latency := res.Latency.Round(time.Millisecond)
		if latency == 0 && res.Latency > 0 {
			latency = res.Latency.Round(time.Microsecond)
		}
		var modelsDesc string
		if len(res.Models) > 0 {
			modelsDesc = strings.Join(res.Models, ", ")
		} else {
			modelsDesc = "none"
		}
		msg := fmt.Sprintf("Gateway reachable at %s (latency: %v, engine: %s, models: %s)", res.URL, latency, res.Engine, modelsDesc)
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Model Gateway",
			Status:  StatusOK,
			Message: msg,
		})
		return
	}

	errMsg := ""
	if res != nil && res.Error != "" {
		errMsg = res.Error
	} else if err != nil {
		errMsg = err.Error()
	}

	var msg string
	hint := "Start local inference server or verify port"
	if errMsg != "" {
		msg = fmt.Sprintf("Gateway unreachable at %s: %s (hint: %s)", modelURL, errMsg, hint)
	} else {
		msg = fmt.Sprintf("Gateway unreachable at %s (hint: %s)", modelURL, hint)
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Model Gateway",
		Status:  StatusWarning,
		Message: msg,
	})
	report.WarningCount++
}
