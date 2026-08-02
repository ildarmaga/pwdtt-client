package wbxray

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Runner manages an xray-core subprocess with TUN inbound.
type Runner struct {
	cfg Config

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	workDir string
	logFn   func(string, ...any)
	exitCh  chan struct{}
	exitErr error
}

func NewRunner(cfg Config, logFn func(string, ...any)) *Runner {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	return &Runner{cfg: cfg.normalized(), logFn: logFn}
}

// Launch writes config, starts xray, and returns without waiting for exit.
func (r *Runner) Launch(ctx context.Context, binaryPath string) error {
	if binaryPath == "" {
		return fmt.Errorf("wbxray: binary path required")
	}
	binaryPath, err := filepath.Abs(binaryPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("wbxray: binary %s: %w", binaryPath, err)
	}

	workDir, err := os.MkdirTemp("", "wdtt-xray-*")
	if err != nil {
		return err
	}
	r.workDir = workDir

	raw, err := BuildConfigJSON(r.cfg)
	if err != nil {
		r.cleanupWorkDir()
		return err
	}
	cfgPath := filepath.Join(workDir, "config.json")
	if err := os.WriteFile(cfgPath, raw, 0644); err != nil {
		r.cleanupWorkDir()
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.exitCh != nil {
		select {
		case <-r.exitCh:
		default:
		}
	}
	r.cancel = cancel
	r.exitCh = make(chan struct{})
	r.exitErr = nil
	r.mu.Unlock()

	cmd := exec.CommandContext(runCtx, binaryPath, "run", "-c", cfgPath)
	cmd.Dir = filepath.Dir(binaryPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		r.cleanupWorkDir()
		return fmt.Errorf("wbxray: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		r.cleanupWorkDir()
		return fmt.Errorf("wbxray: stderr pipe: %w", err)
	}
	cmd.Env = append(os.Environ(),
		"XRAY_LOCATION_ASSET="+filepath.Dir(binaryPath),
	)
	if assetDir := geoAssetDir(binaryPath); assetDir != "" {
		cmd.Env = append(cmd.Env, "XRAY_LOCATION_ASSET="+assetDir)
	}
	hideConsole(cmd)

	r.logFn("[xray] starting mode=%s tun=%s socks=%s:%d rules=%d",
		r.cfg.Mode, r.cfg.AdapterName, r.cfg.SocksHost, r.cfg.SocksPort, countRoutingRules(raw))

	r.mu.Lock()
	r.cmd = cmd
	r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		r.cleanupWorkDir()
		return fmt.Errorf("wbxray: start: %w", err)
	}
	go r.pipeXrayLog(stdout)
	go r.pipeXrayLog(stderr)

	go func() {
		waitErr := cmd.Wait()
		r.cleanupWorkDir()
		r.mu.Lock()
		r.exitErr = waitErr
		close(r.exitCh)
		r.cmd = nil
		r.mu.Unlock()
	}()
	return nil
}

// Exited reports whether xray has exited and the wait error if any.
func (r *Runner) Exited() (error, bool) {
	r.mu.Lock()
	ch := r.exitCh
	err := r.exitErr
	r.mu.Unlock()
	if ch == nil {
		return nil, false
	}
	select {
	case <-ch:
		return err, true
	default:
		return nil, false
	}
}

// Start writes config.json, launches xray, blocks until the process exits or ctx is done.
func (r *Runner) Start(ctx context.Context, binaryPath string) error {
	if err := r.Launch(ctx, binaryPath); err != nil {
		return err
	}

	r.mu.Lock()
	ch := r.exitCh
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		r.Stop()
		return ctx.Err()
	case <-ch:
		r.mu.Lock()
		err := r.exitErr
		r.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("wbxray: exited: %w", err)
		}
		return ctx.Err()
	}
}

func (r *Runner) pipeXrayLog(rd interface{ Read([]byte) (int, error) }) {
	buf := make([]byte, 4096)
	for {
		n, readErr := rd.Read(buf)
		if n > 0 {
			line := strings.TrimSpace(string(buf[:n]))
			// Access log: one line per flow — skip before string work / relay logFn.
			if strings.Contains(line, " accepted ") {
				continue
			}
			r.logFn("[xray] %s", line)
		}
		if readErr != nil {
			return
		}
	}
}

// Stop terminates the xray subprocess.
func (r *Runner) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	cmd := r.cmd
	r.cancel = nil
	r.cmd = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	r.cleanupWorkDir()
}

func (r *Runner) cleanupWorkDir() {
	if r.workDir == "" {
		return
	}
	_ = os.RemoveAll(r.workDir)
	r.workDir = ""
}

// geoAssetDir returns a directory containing geoip.dat if present beside binary or in xray/ subdir.
func geoAssetDir(binaryPath string) string {
	dir := filepath.Dir(binaryPath)
	candidates := []string{
		dir,
		filepath.Join(dir, "xray"),
	}
	for _, d := range candidates {
		if _, err := os.Stat(filepath.Join(d, "geoip.dat")); err == nil {
			return d
		}
	}
	return ""
}

func countRoutingRules(configJSON []byte) int {
	var doc struct {
		Routing struct {
			Rules []interface{} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(configJSON, &doc); err != nil {
		return 0
	}
	return len(doc.Routing.Rules)
}
