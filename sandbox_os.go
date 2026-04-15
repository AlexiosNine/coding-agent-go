package cc

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OSSandbox provides OS-level isolation for shell command execution.
// Uses platform-specific mechanisms:
//   - macOS: sandbox-exec with Seatbelt profiles
//   - Linux: Docker containers with bind mounts
//
// This is opt-in and requires external dependencies (Docker on Linux).
// Falls back to application-level sandbox if OS sandbox is unavailable.
type OSSandbox struct {
	AllowedPaths []string // directories accessible to sandboxed commands
	WorkDir      string   // working directory for commands
}

// NewOSSandbox creates an OS-level sandbox restricted to the given paths.
func NewOSSandbox(allowedPaths ...string) *OSSandbox {
	if len(allowedPaths) == 0 {
		allowedPaths = []string{"."}
	}
	// Convert to absolute paths
	for i, p := range allowedPaths {
		abs, err := filepath.Abs(p)
		if err == nil {
			allowedPaths[i] = abs
		}
	}
	return &OSSandbox{
		AllowedPaths: allowedPaths,
		WorkDir:      allowedPaths[0],
	}
}

// WrapCommand wraps a shell command with OS-level sandboxing.
// Returns an exec.Cmd ready to run, or error if sandboxing is unavailable.
func (s *OSSandbox) WrapCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return s.wrapSeatbelt(ctx, command)
	case "linux":
		return s.wrapDocker(ctx, command)
	default:
		return nil, fmt.Errorf("OS sandbox not supported on %s", runtime.GOOS)
	}
}

// wrapSeatbelt uses macOS sandbox-exec with a Seatbelt profile.
func (s *OSSandbox) wrapSeatbelt(ctx context.Context, command string) (*exec.Cmd, error) {
	// Build Seatbelt profile
	var allowRules strings.Builder
	for _, path := range s.AllowedPaths {
		allowRules.WriteString(fmt.Sprintf(`(allow file-read* (subpath "%s"))`, path))
		allowRules.WriteString("\n")
		allowRules.WriteString(fmt.Sprintf(`(allow file-write* (subpath "%s"))`, path))
		allowRules.WriteString("\n")
	}

	profile := fmt.Sprintf(`(version 1)
(deny default)
%s
(allow process-exec*)
(allow process-fork)
(allow file-read-metadata)
(allow file-read* (literal "/bin/sh") (literal "/usr/bin/env"))
(allow file-read* (subpath "/usr/lib") (subpath "/System/Library"))
(deny network*)
(allow network* (remote ip "localhost:*"))
(allow sysctl-read)
`, allowRules.String())

	cmd := exec.CommandContext(ctx, "sandbox-exec", "-p", profile, "/bin/sh", "-c", command)
	cmd.Dir = s.WorkDir
	return cmd, nil
}

// wrapDocker uses Docker to run the command in an isolated container.
func (s *OSSandbox) wrapDocker(ctx context.Context, command string) (*exec.Cmd, error) {
	// Check if Docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found: %w (install Docker to use OS sandbox on Linux)", err)
	}

	// Build docker run arguments
	args := []string{
		"run",
		"--rm",                    // remove container after execution
		"--network=none",          // no network access
		"-w", "/workspace",        // working directory inside container
		"-v", s.WorkDir + ":/workspace", // bind mount workspace
	}

	// Add additional bind mounts for other allowed paths
	for _, path := range s.AllowedPaths {
		if path != s.WorkDir {
			mountPoint := "/mnt/" + filepath.Base(path)
			args = append(args, "-v", path+":"+mountPoint)
		}
	}

	args = append(args, "alpine:latest", "/bin/sh", "-c", command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd, nil
}

// IsAvailable checks if OS-level sandboxing is available on this system.
func (s *OSSandbox) IsAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := exec.LookPath("sandbox-exec")
		return err == nil
	case "linux":
		_, err := exec.LookPath("docker")
		return err == nil
	default:
		return false
	}
}

// Context key for OSSandbox
type osSandboxKey struct{}

// WithOSSandbox attaches an OSSandbox to the context.
func WithOSSandbox(ctx context.Context, s *OSSandbox) context.Context {
	return context.WithValue(ctx, osSandboxKey{}, s)
}

// GetOSSandbox retrieves the OSSandbox from context, or nil if not set.
func GetOSSandbox(ctx context.Context) *OSSandbox {
	if s, ok := ctx.Value(osSandboxKey{}).(*OSSandbox); ok {
		return s
	}
	return nil
}
