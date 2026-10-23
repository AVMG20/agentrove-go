package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"

	"agentrove/internal/config"

	"github.com/creack/pty"
)

// HostProvider runs everything as the API's own OS user, directly on the
// workspace directory. Used when Docker is absent or not wanted.
type HostProvider struct {
	cfg *config.Config
}

func NewHostProvider(cfg *config.Config) *HostProvider { return &HostProvider{cfg: cfg} }

func (h *HostProvider) Kind() string { return KindHost }

func (h *HostProvider) Create(ctx context.Context, opts CreateOptions) error {
	if opts.WorkspacePath == "" {
		return errors.New("workspace path is required for host sandboxes")
	}
	return os.MkdirAll(opts.WorkspacePath, 0o755)
}

func (h *HostProvider) Delete(ctx context.Context, sandboxID string) error { return nil }

func (h *HostProvider) EnsureRunning(ctx context.Context, sandboxID string, opts CreateOptions) error {
	if opts.WorkspacePath != "" {
		if _, err := os.Stat(opts.WorkspacePath); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

func (h *HostProvider) Stop(ctx context.Context, sandboxID string) error { return nil }

func (h *HostProvider) Running(ctx context.Context) ([]string, error) { return nil, nil }

func (h *HostProvider) DiskUsage(ctx context.Context, sandboxID string) (int64, bool) {
	return 0, false
}

func (h *HostProvider) command(ctx context.Context, req ExecRequest) *exec.Cmd {
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, req.Cmd[0], req.Cmd[1:]...)
	} else {
		cmd = exec.Command(req.Cmd[0], req.Cmd[1:]...)
	}
	cmd.Dir = req.Cwd
	cmd.Env = hostEnv(req.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// hostEnv inherits the API's env (auth, proxies, PATH) and layers req on top.
func hostEnv(extra map[string]string) []string {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				merged[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (h *HostProvider) Exec(ctx context.Context, sandboxID string, req ExecRequest) (ExecResult, error) {
	cmd := h.command(ctx, req)
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &stdout, &stderr, req.Stdin
	err := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, nil
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

type hostProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (h *HostProvider) Start(ctx context.Context, sandboxID string, req ExecRequest) (Process, error) {
	cmd := h.command(nil, req)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &hostProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (p *hostProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *hostProcess) Stdout() io.Reader     { return p.stdout }
func (p *hostProcess) Stderr() io.Reader     { return p.stderr }
func (p *hostProcess) Wait() error           { return p.cmd.Wait() }
func (p *hostProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	go func() {
		time.Sleep(3 * time.Second)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()
	return nil
}

type hostPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (h *HostProvider) StartPTY(ctx context.Context, sandboxID string, req ExecRequest, rows, cols int) (PTY, error) {
	cmd := h.command(nil, req)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	size := &pty.Winsize{Rows: uint16(max(rows, 1)), Cols: uint16(max(cols, 1))}
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, err
	}
	return &hostPTY{f: f, cmd: cmd}, nil
}

func (p *hostPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *hostPTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *hostPTY) Close() error {
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
	}
	return p.f.Close()
}
func (p *hostPTY) Resize(rows, cols int) error {
	return pty.Setsize(p.f, &pty.Winsize{Rows: uint16(max(rows, 1)), Cols: uint16(max(cols, 1))})
}
func (p *hostPTY) Wait() error { return p.cmd.Wait() }
