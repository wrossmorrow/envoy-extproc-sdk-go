package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// EnvoyConfig configures how StartEnvoyRunner launches Envoy.
type EnvoyConfig struct {
	Version               string
	PrimaryUpstreamPort   int
	SecondaryUpstreamPort int
	ExtProcPort           int
}

// EnvoyRunner manages a live Envoy proxy instance (either containerized or via
// ENVOY_BIN) for conformance testing.
type EnvoyRunner struct {
	version       string
	containerName string
	cmd           *exec.Cmd
	logBuf        *syncBuffer
	proxyAddr     string
	adminAddr     string
	exitCh        chan error
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func pickFreePort() (int, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer lis.Close()
	addr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected address %T", lis.Addr())
	}
	return addr.Port, nil
}

// CheckDockerAvailable reports whether the local Docker daemon is reachable.
func CheckDockerAvailable(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(dctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker unavailable: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// StartEnvoyRunner launches Envoy using ENVOY_BIN when set, or Docker
// envoyproxy/envoy:<Version> otherwise.
func StartEnvoyRunner(ctx context.Context, cfg EnvoyConfig) (*EnvoyRunner, error) {
	if cfg.Version == "" {
		v, err := ResolveEnvoyVersion()
		if err != nil {
			return nil, err
		}
		cfg.Version = v
	}

	if envoyBin := os.Getenv("ENVOY_BIN"); envoyBin != "" {
		return startBinaryEnvoy(ctx, envoyBin, cfg)
	}
	return startDockerEnvoy(ctx, cfg)
}

func startBinaryEnvoy(ctx context.Context, envoyBin string, cfg EnvoyConfig) (*EnvoyRunner, error) {
	listenerPort, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("pick listener port: %w", err)
	}
	adminPort, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("pick admin port: %w", err)
	}

	_, rawJSON, err := BuildBootstrap(BootstrapParams{
		UpstreamHost:          "127.0.0.1",
		PrimaryUpstreamPort:   cfg.PrimaryUpstreamPort,
		SecondaryUpstreamPort: cfg.SecondaryUpstreamPort,
		ExtProcPort:           cfg.ExtProcPort,
		ListenerPort:          listenerPort,
		AdminPort:             adminPort,
	})
	if err != nil {
		return nil, err
	}

	logBuf := &syncBuffer{}
	cmd := exec.Command(envoyBin,
		"--config-yaml", string(rawJSON),
		"--log-level", "warn",
		"--concurrency", "2",
	)
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", envoyBin, err)
	}

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	runner := &EnvoyRunner{
		version:   cfg.Version,
		cmd:       cmd,
		logBuf:    logBuf,
		proxyAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(listenerPort)),
		adminAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(adminPort)),
		exitCh:    exitCh,
	}

	if werr := runner.waitReady(ctx, 30*time.Second); werr != nil {
		_ = runner.Close()
		return nil, fmt.Errorf("envoy binary failed readiness: %w\nlogs:\n%s", werr, logBuf.String())
	}
	return runner, nil
}

func startDockerEnvoy(ctx context.Context, cfg EnvoyConfig) (*EnvoyRunner, error) {
	if err := CheckDockerAvailable(ctx); err != nil {
		return nil, err
	}

	const containerListenerPort = 18080
	const containerAdminPort = 19901

	_, rawJSON, err := BuildBootstrap(BootstrapParams{
		UpstreamHost:          "host.docker.internal",
		PrimaryUpstreamPort:   cfg.PrimaryUpstreamPort,
		SecondaryUpstreamPort: cfg.SecondaryUpstreamPort,
		ExtProcPort:           cfg.ExtProcPort,
		ListenerPort:          containerListenerPort,
		AdminPort:             containerAdminPort,
	})
	if err != nil {
		return nil, err
	}

	image := fmt.Sprintf("envoyproxy/envoy:%s", cfg.Version)
	containerName := fmt.Sprintf("extproc-conformance-%d-%d", os.Getpid(), time.Now().UnixNano())

	runCmd := exec.CommandContext(ctx, "docker", "run",
		"-d", "--rm",
		"--name", containerName,
		"--add-host=host.docker.internal:host-gateway",
		"-p", fmt.Sprintf("127.0.0.1::%d", containerListenerPort),
		"-p", fmt.Sprintf("127.0.0.1::%d", containerAdminPort),
		image,
		"envoy",
		"--config-yaml", string(rawJSON),
		"--log-level", "warn",
		"--concurrency", "2",
	)
	out, err := runCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run %s failed: %w\noutput: %s", image, err, strings.TrimSpace(string(out)))
	}

	runner := &EnvoyRunner{
		version:       cfg.Version,
		containerName: containerName,
	}

	listenerHostPort, lerr := resolveDockerPort(ctx, containerName, containerListenerPort)
	if lerr != nil {
		_ = runner.Close()
		return nil, lerr
	}
	adminHostPort, aerr := resolveDockerPort(ctx, containerName, containerAdminPort)
	if aerr != nil {
		_ = runner.Close()
		return nil, aerr
	}

	runner.proxyAddr = net.JoinHostPort("127.0.0.1", listenerHostPort)
	runner.adminAddr = net.JoinHostPort("127.0.0.1", adminHostPort)

	if werr := runner.waitReady(ctx, 45*time.Second); werr != nil {
		logsOut, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
		_ = runner.Close()
		return nil, fmt.Errorf("envoy container %s failed readiness: %w\nlogs:\n%s", image, werr, string(logsOut))
	}
	return runner, nil
}

func resolveDockerPort(ctx context.Context, containerName string, containerPort int) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "port", containerName, fmt.Sprintf("%d/tcp", containerPort)).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker port %d for %s: %w (%s)", containerPort, containerName, err, strings.TrimSpace(string(out)))
	}
	firstLine := strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
	_, port, err := net.SplitHostPort(firstLine)
	if err != nil {
		return "", fmt.Errorf("parse docker port output %q: %w", firstLine, err)
	}
	return port, nil
}

func (r *EnvoyRunner) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	readyURL := r.AdminURL() + "/ready"

	for {
		if r.exitCh != nil {
			select {
			case err := <-r.exitCh:
				return fmt.Errorf("envoy exited early: %v", err)
			default:
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err == nil {
			resp, derr := client.Do(req)
			if derr == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", readyURL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// ProxyAddr returns the host:port address of Envoy's HTTP listener.
func (r *EnvoyRunner) ProxyAddr() string {
	return r.proxyAddr
}

// ProxyURL returns the base http://host:port URL of Envoy's HTTP listener.
func (r *EnvoyRunner) ProxyURL() string {
	return "http://" + r.proxyAddr
}

// AdminURL returns the base http://host:port URL of Envoy's admin interface.
func (r *EnvoyRunner) AdminURL() string {
	return "http://" + r.adminAddr
}

// ExtProcStats queries Envoy's admin /stats endpoint for ext_proc counters and gauges.
func (r *EnvoyRunner) ExtProcStats(ctx context.Context) (map[string]uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.AdminURL()+"/stats?filter=ext_proc&format=json", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("envoy admin stats status %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Stats []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode envoy stats JSON: %w", err)
	}

	out := make(map[string]uint64, len(payload.Stats))
	for _, entry := range payload.Stats {
		var val uint64
		if len(entry.Value) > 0 && json.Unmarshal(entry.Value, &val) == nil {
			out[entry.Name] = val
		}
	}
	return out, nil
}

// AssertNoProtocolErrors verifies Envoy's ext_proc filter recorded zero
// spurious messages across the conformance run.
func (r *EnvoyRunner) AssertNoProtocolErrors(t *testing.T) {
	t.Helper()
	stats, err := r.ExtProcStats(context.Background())
	if err != nil {
		t.Fatalf("query Envoy admin ext_proc stats: %v", err)
	}
	var checked int
	for name, val := range stats {
		if strings.HasSuffix(name, ".spurious_msgs_received") {
			checked++
			if val != 0 {
				t.Errorf("Envoy stat %s = %d, want 0", name, val)
			}
		}
	}
	if checked == 0 {
		t.Logf("no *.spurious_msgs_received counter emitted in Envoy stats (%d scalar stats collected)", len(stats))
	}
}

// Close stops the Envoy container or child process.
func (r *EnvoyRunner) Close() error {
	if r.containerName != "" {
		out, err := exec.Command("docker", "rm", "-f", r.containerName).CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker rm -f %s: %w (%s)", r.containerName, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		if r.exitCh != nil {
			<-r.exitCh
		}
	}
	return nil
}
