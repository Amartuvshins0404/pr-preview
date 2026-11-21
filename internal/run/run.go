package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

var hidden = []string{"GITHUB_TOKEN", "ACCESS_PASSWORD"}

func BaseEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		keep := true
		for _, h := range hidden {
			if strings.HasPrefix(kv, h+"=") {
				keep = false
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

type Cmd struct {
	Dir string
	Env []string
	Out io.Writer
}

func (c Cmd) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = c.Dir
	cmd.Env = append(BaseEnv(), c.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 10 * time.Second
	if c.Out != nil {
		fmt.Fprintf(c.Out, "$ %s %s\n", name, strings.Join(args, " "))
		cmd.Stdout = c.Out
		cmd.Stderr = c.Out
	}
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%s: %w", name, ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", name, firstArg(args), err)
	}
	return nil
}

func (c Cmd) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = c.Dir
	cmd.Env = append(BaseEnv(), c.Env...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, firstArg(args), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

type Redactor struct {
	mu      sync.Mutex
	w       io.Writer
	secrets []string
}

func NewRedactor(w io.Writer, secrets ...string) *Redactor {
	r := &Redactor{w: w}
	for _, s := range secrets {
		if len(s) >= 4 {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := p
	for _, s := range r.secrets {
		b = bytes.ReplaceAll(b, []byte(s), []byte("***"))
	}
	if _, err := r.w.Write(b); err != nil {
		return 0, err
	}
	return len(p), nil
}
