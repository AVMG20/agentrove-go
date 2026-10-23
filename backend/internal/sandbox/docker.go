package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentrove/internal/config"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
)

type DockerProvider struct {
	cfg *config.Config
	cli *client.Client

	netOnce sync.Once
}

func NewDockerProvider(cfg *config.Config) (*DockerProvider, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if cfg.DockerHost != "" {
		opts = append(opts, client.WithHost(cfg.DockerHost))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("docker daemon unreachable: %w", err)
	}
	return &DockerProvider{cfg: cfg, cli: cli}, nil
}

func (d *DockerProvider) Kind() string { return KindDocker }

func containerName(id string) string { return ContainerPrefix + id }

// hostPath translates an API-visible storage path to the daemon's view.
func (d *DockerProvider) hostPath(p string) string {
	if d.cfg.HostStoragePath == d.cfg.StoragePath {
		return p
	}
	if rel, err := filepath.Rel(d.cfg.StoragePath, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join(d.cfg.HostStoragePath, rel)
	}
	return p
}

func (d *DockerProvider) ensureNetwork(ctx context.Context) {
	d.netOnce.Do(func() {
		if d.cfg.DockerNetwork == "" {
			return
		}
		if _, err := d.cli.NetworkInspect(ctx, d.cfg.DockerNetwork, network.InspectOptions{}); err == nil {
			return
		}
		if _, err := d.cli.NetworkCreate(ctx, d.cfg.DockerNetwork, network.CreateOptions{Driver: "bridge"}); err != nil {
			slog.Warn("create docker network", "network", d.cfg.DockerNetwork, "err", err)
		}
	})
}

// claudeConfigDir is the per-user Claude config shared by all of the user's
// sandboxes, so logging in once covers every workspace.
func (d *DockerProvider) claudeConfigDir(userID string) string {
	dir := filepath.Join(d.cfg.AgentHomeDir(userID), "claude")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.Chown(dir, ContainerUID, ContainerUID)
	_ = os.Chown(filepath.Dir(dir), ContainerUID, ContainerUID)
	return dir
}

func (d *DockerProvider) Create(ctx context.Context, opts CreateOptions) error {
	d.ensureNetwork(ctx)
	hc := &container.HostConfig{
		NetworkMode: container.NetworkMode(d.cfg.DockerNetwork),
		SecurityOpt: []string{"no-new-privileges=false"},
		Init:        boolPtr(true),
	}
	if d.cfg.DockerMemLimit != "" {
		if n, err := parseByteSize(d.cfg.DockerMemLimit); err == nil {
			hc.Memory = n
		}
	}
	if d.cfg.DockerCPUPeriod > 0 {
		hc.CPUPeriod = d.cfg.DockerCPUPeriod
	}
	if d.cfg.DockerCPUQuota > 0 {
		hc.CPUQuota = d.cfg.DockerCPUQuota
	}
	if d.cfg.DockerPidsLimit > 0 {
		hc.PidsLimit = &d.cfg.DockerPidsLimit
	}
	if opts.WorkspacePath != "" {
		_ = chownTree(opts.WorkspacePath)
		hc.Binds = append(hc.Binds, d.hostPath(opts.WorkspacePath)+":"+ContainerWorkspace)
	}
	if opts.UserID != "" {
		hc.Binds = append(hc.Binds, d.hostPath(d.claudeConfigDir(opts.UserID))+":"+ContainerHome+"/.claude")
	}
	env := []string{
		"TERM=" + TerminalType,
		"HOME=" + ContainerHome,
		"USER=" + ContainerUser,
		"CLAUDE_CONFIG_DIR=" + ContainerHome + "/.claude",
	}
	if d.cfg.DockerNodeMaxOldMB > 0 {
		env = append(env, fmt.Sprintf("NODE_OPTIONS=--max-old-space-size=%d", d.cfg.DockerNodeMaxOldMB))
	}
	cc := &container.Config{
		Image:      d.cfg.DockerImage,
		Cmd:        []string{"sleep", "infinity"},
		Hostname:   "sandbox",
		User:       ContainerUser,
		WorkingDir: ContainerWorkspace,
		Env:        env,
		Labels: map[string]string{
			ManagedLabel:        "true",
			"agentrove.sandbox": opts.SandboxID,
			"agentrove.user":    opts.UserID,
		},
	}
	name := containerName(opts.SandboxID)
	_ = d.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if _, err := d.cli.ContainerCreate(ctx, cc, hc, nil, nil, name); err != nil {
		return fmt.Errorf("create sandbox container: %w", err)
	}
	if err := d.cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		return fmt.Errorf("start sandbox container: %w", err)
	}
	return nil
}

func (d *DockerProvider) Delete(ctx context.Context, sandboxID string) error {
	err := d.cli.ContainerRemove(ctx, containerName(sandboxID), container.RemoveOptions{Force: true})
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

func (d *DockerProvider) EnsureRunning(ctx context.Context, sandboxID string, opts CreateOptions) error {
	info, err := d.cli.ContainerInspect(ctx, containerName(sandboxID))
	if errdefs.IsNotFound(err) {
		// Container was removed out-of-band (or the image was rebuilt and
		// pruned): recreate it — workspace files live on the host.
		slog.Info("recreating missing sandbox container", "sandbox", sandboxID)
		return d.Create(ctx, opts)
	}
	if err != nil {
		return err
	}
	if info.State != nil && info.State.Running {
		if info.State.Paused {
			return d.cli.ContainerUnpause(ctx, info.ID)
		}
		return nil
	}
	slog.Info("starting sandbox container", "sandbox", sandboxID)
	return d.cli.ContainerStart(ctx, info.ID, container.StartOptions{})
}

func (d *DockerProvider) Stop(ctx context.Context, sandboxID string) error {
	timeout := 10
	err := d.cli.ContainerStop(ctx, containerName(sandboxID), container.StopOptions{Timeout: &timeout})
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

func (d *DockerProvider) Running(ctx context.Context) ([]string, error) {
	list, err := d.cli.ContainerList(ctx, container.ListOptions{Filters: filters.NewArgs(filters.Arg("label", ManagedLabel+"=true"))})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range list {
		if c.State != "running" {
			continue
		}
		if id := c.Labels["agentrove.sandbox"]; id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (d *DockerProvider) DiskUsage(ctx context.Context, sandboxID string) (int64, bool) {
	info, _, err := d.cli.ContainerInspectWithRaw(ctx, containerName(sandboxID), true)
	if err != nil || info.SizeRw == nil {
		return 0, false
	}
	return *info.SizeRw, true
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (d *DockerProvider) execOptions(req ExecRequest, tty, stdin bool) container.ExecOptions {
	user := ContainerUser
	if req.Root {
		user = "root"
	}
	return container.ExecOptions{
		User:         user,
		Tty:          tty,
		AttachStdin:  stdin,
		AttachStdout: true,
		AttachStderr: true,
		Env:          envList(req.Env),
		WorkingDir:   req.Cwd,
		Cmd:          req.Cmd,
	}
}

func (d *DockerProvider) Exec(ctx context.Context, sandboxID string, req ExecRequest) (ExecResult, error) {
	created, err := d.cli.ContainerExecCreate(ctx, containerName(sandboxID), d.execOptions(req, false, req.Stdin != nil))
	if err != nil {
		return ExecResult{}, err
	}
	att, err := d.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, err
	}
	defer att.Close()
	if req.Stdin != nil {
		go func() {
			_, _ = io.Copy(att.Conn, req.Stdin)
			_ = att.CloseWrite()
		}()
	}
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&stdout, &stderr, att.Reader)
		done <- err
	}()
	select {
	case <-ctx.Done():
		att.Close()
		return ExecResult{ExitCode: -1, Stdout: stdout.String(), Stderr: stderr.String()}, ctx.Err()
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			return ExecResult{}, err
		}
	}
	insp, err := d.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{ExitCode: insp.ExitCode, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

// dockerProcess is a long-lived exec with demultiplexed stdio. The command is
// wrapped so it records its pid, letting Kill signal it inside the container.
type dockerProcess struct {
	d        *DockerProvider
	sandbox  string
	execID   string
	pidFile  string
	conn     io.WriteCloser
	closeAll func()
	stdout   *io.PipeReader
	stderr   *io.PipeReader
	done     chan error
}

type halfCloser struct {
	write func([]byte) (int, error)
	close func() error
}

func (h halfCloser) Write(p []byte) (int, error) { return h.write(p) }
func (h halfCloser) Close() error                { return h.close() }

func (d *DockerProvider) Start(ctx context.Context, sandboxID string, req ExecRequest) (Process, error) {
	pidFile := "/tmp/agentrove-" + uuid.NewString()[:8] + ".pid"
	wrapped := req
	wrapped.Cmd = append([]string{"sh", "-c", `echo $$ > "$0"; exec "$@"`, pidFile}, req.Cmd...)
	// Background context: the process outlives the request that started it.
	created, err := d.cli.ContainerExecCreate(context.Background(), containerName(sandboxID), d.execOptions(wrapped, false, true))
	if err != nil {
		return nil, err
	}
	att, err := d.cli.ContainerExecAttach(context.Background(), created.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	p := &dockerProcess{d: d, sandbox: sandboxID, execID: created.ID, pidFile: pidFile, stdout: outR, stderr: errR, done: make(chan error, 1)}
	p.conn = halfCloser{write: att.Conn.Write, close: att.CloseWrite}
	p.closeAll = att.Close
	go func() {
		_, err := stdcopy.StdCopy(outW, errW, att.Reader)
		outW.Close()
		errW.Close()
		att.Close()
		if err == nil || errors.Is(err, io.EOF) {
			insp, ierr := d.cli.ContainerExecInspect(context.Background(), created.ID)
			if ierr == nil && insp.ExitCode != 0 {
				err = fmt.Errorf("exit status %d", insp.ExitCode)
			} else {
				err = nil
			}
		}
		p.done <- err
		close(p.done)
	}()
	return p, nil
}

func (p *dockerProcess) Stdin() io.WriteCloser { return p.conn }
func (p *dockerProcess) Stdout() io.Reader     { return p.stdout }
func (p *dockerProcess) Stderr() io.Reader     { return p.stderr }

func (p *dockerProcess) Wait() error {
	err, ok := <-p.done
	if !ok {
		return nil
	}
	return err
}

func (p *dockerProcess) Kill() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := fmt.Sprintf(`pid=$(cat %[1]s 2>/dev/null) && [ -n "$pid" ] && { pkill -TERM -P "$pid" 2>/dev/null; kill -TERM "$pid" 2>/dev/null; sleep 2; pkill -KILL -P "$pid" 2>/dev/null; kill -KILL "$pid" 2>/dev/null; }; rm -f %[1]s`, p.pidFile)
	_, err := p.d.Exec(ctx, p.sandbox, ExecRequest{Cmd: []string{"sh", "-c", script}, Root: true})
	p.closeAll()
	return err
}

type dockerPTY struct {
	d      *DockerProvider
	execID string
	conn   io.ReadWriteCloser
	done   chan struct{}
}

func (d *DockerProvider) StartPTY(ctx context.Context, sandboxID string, req ExecRequest, rows, cols int) (PTY, error) {
	opts := d.execOptions(req, true, true)
	if rows > 0 && cols > 0 {
		opts.ConsoleSize = &[2]uint{uint(rows), uint(cols)}
	}
	created, err := d.cli.ContainerExecCreate(context.Background(), containerName(sandboxID), opts)
	if err != nil {
		return nil, err
	}
	att, err := d.cli.ContainerExecAttach(context.Background(), created.ID, container.ExecAttachOptions{Tty: true, ConsoleSize: opts.ConsoleSize})
	if err != nil {
		return nil, err
	}
	p := &dockerPTY{d: d, execID: created.ID, done: make(chan struct{})}
	p.conn = &hijackedRW{r: att.Reader, w: att.Conn, close: att.Close}
	return p, nil
}

type hijackedRW struct {
	r     io.Reader
	w     io.Writer
	close func()
	once  sync.Once
}

func (h *hijackedRW) Read(b []byte) (int, error)  { return h.r.Read(b) }
func (h *hijackedRW) Write(b []byte) (int, error) { return h.w.Write(b) }
func (h *hijackedRW) Close() error {
	h.once.Do(h.close)
	return nil
}

func (p *dockerPTY) Read(b []byte) (int, error) {
	n, err := p.conn.Read(b)
	if err != nil {
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
	return n, err
}
func (p *dockerPTY) Write(b []byte) (int, error) { return p.conn.Write(b) }
func (p *dockerPTY) Close() error                { return p.conn.Close() }
func (p *dockerPTY) Resize(rows, cols int) error {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	return p.d.cli.ContainerExecResize(context.Background(), p.execID, container.ResizeOptions{Height: uint(rows), Width: uint(cols)})
}
func (p *dockerPTY) Wait() error {
	<-p.done
	return nil
}

func parseByteSize(s string) (int64, error) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "b")
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k':
		mult = 1 << 10
	case 'm':
		mult = 1 << 20
	case 'g':
		mult = 1 << 30
	}
	if mult > 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n * mult, err
}

func boolPtr(b bool) *bool { return &b }

// chownTree hands a workspace to the sandbox user. Best effort: it only
// succeeds when the API runs as root (the Docker deployment).
func chownTree(root string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Lchown(p, ContainerUID, ContainerUID)
		return nil
	})
}
