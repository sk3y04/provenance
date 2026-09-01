package encode

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// CommandRunner abstracts the external-process surface the encode package
// depends on (ffmpeg / ffprobe). Injecting it lets the fan-out, logging, and
// encoder-availability logic be exercised in tests with a fake runner and no
// real GPU hardware — mirroring how internal/extractor wraps exec.Command.
type CommandRunner interface {
	// Run executes bin with args (preceded by env appended to the process
	// environment), streaming stdout to stdout and stderr to stderr. Both
	// writers may be nil. It returns the first non-nil error: exec failure or
	// a non-zero exit status.
	Run(ctx context.Context, bin string, env []string, stdout, stderr io.Writer, args ...string) error
}

// execRunner is the production CommandRunner backed by os/exec.
type execRunner struct{}

// NewRunner returns the production exec-backed CommandRunner.
func NewRunner() CommandRunner { return execRunner{} }

// Run implements CommandRunner.
func (execRunner) Run(ctx context.Context, bin string, env []string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
