// Package tool provides built-in tools for the agent runtime.
package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type shellInput struct {
	Command string `json:"command" desc:"The shell command to execute"`
	Timeout int    `json:"timeout" desc:"Timeout in seconds (default 30)"`
	Offset  int    `json:"offset,omitempty" desc:"Optional: line offset for pagination (0-indexed)"`
	Limit   int    `json:"limit,omitempty" desc:"Optional: maximum lines per page (default 200)"`
}

// Shell returns a tool that executes shell commands.
// If an OSSandbox is available in the context, commands run with OS-level isolation.
// Otherwise, commands run directly (subject to application-level sandbox checks).
func Shell() cc.Tool {
	return cc.NewFuncTool("shell", "Execute a shell command and return its output. Supports pagination via offset and limit for large outputs. If a command returns many lines, use the same command with offset=N to see subsequent pages.", func(ctx context.Context, in shellInput) (string, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}

		// If offset > 0, try to serve from buffer
		if in.Offset > 0 {
			buf := cc.GetOutputBuffer(ctx)
			if buf != nil {
				page, total, exists := buf.TryGetPage(in.Command, in.Offset, limit)
				if exists {
					hasMore := (in.Offset + limit) < total
					if hasMore {
						nextOffset := in.Offset + limit
						return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, total, nextOffset), nil
					}
					return page, nil
				}
			}
		}

		timeout := 30
		if in.Timeout > 0 {
			timeout = in.Timeout
		}

		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		var cmd *exec.Cmd
		var err error

		// Check for OS-level sandbox
		if osSandbox := cc.GetOSSandbox(ctx); osSandbox != nil && osSandbox.IsAvailable() {
			cmd, err = osSandbox.WrapCommand(ctx, in.Command)
			if err != nil {
				return fmt.Sprintf("OS sandbox error: %s", err.Error()), nil
			}
		} else {
			// Fallback to direct execution
			cmd = exec.CommandContext(ctx, "sh", "-c", in.Command)
		}

		out, err := cmd.CombinedOutput()
		result := strings.TrimSpace(string(out))

		if err != nil {
			return fmt.Sprintf("Error: %s\nOutput: %s", err.Error(), result), nil
		}

		// Store in buffer for future pagination
		buf := cc.GetOutputBuffer(ctx)
		if buf != nil {
			buf.Store(in.Command, result)
		}

		// Apply pagination
		lines := strings.Split(result, "\n")
		total := len(lines)
		end := limit
		if end > total {
			end = total
		}

		page := strings.Join(lines[0:end], "\n")
		hasMore := end < total

		if hasMore {
			return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, total, end), nil
		}

		return page, nil
	})
}
