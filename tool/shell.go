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
}

// Shell returns a tool that executes shell commands.
// If an OSSandbox is available in the context, commands run with OS-level isolation.
// Otherwise, commands run directly (subject to application-level sandbox checks).
func Shell() cc.Tool {
	return cc.NewFuncTool("shell", "Execute a shell command and return its output", func(ctx context.Context, in shellInput) (string, error) {
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
		return result, nil
	})
}
