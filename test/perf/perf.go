//go:build slow

// Package perf holds shared helpers for the slow-tagged stress tests
// in this directory (crash_recovery_test.go, ch_outage_test.go,
// disk_full_test.go). Build tag `slow` keeps these out of the
// default `make test-integration` run; invoke them via the dedicated
// Makefile targets (`make crash-test`, `make disk-full-test`).
package perf

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// CHAddr returns the integration ClickHouse address (matches
// deploy/docker-compose.dev.yml).
const CHAddr = "127.0.0.1:19000"

// PerfHTTPAddr is the bind address every perf test uses for the
// statnive-live binary it spawns. Avoids the dev port (8080) so a
// running dev binary doesn't collide with the test.
const PerfHTTPAddr = "127.0.0.1:18080"

// BinaryPath returns the absolute path to the statnive-live binary,
// building it if needed. The build is deterministic — repeated
// invocations are cheap (Go's build cache).
func BinaryPath(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "statnive-live")

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	cmd := exec.Command("go", "build", "-mod=vendor", "-o", bin, "./cmd/statnive-live")
	cmd.Dir = repoRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}

	return bin
}

// SpawnBinary starts the statnive-live binary as a subprocess with the
// given env-var overrides. Waits for /healthz to return 200 before
// returning. Caller MUST call cancel() to terminate cleanly (or .Process.Kill
// for a SIGKILL crash test).
//
// The binary's working directory is set to the repo root so relative
// config paths (./config/sources.yaml) resolve correctly. Stdout and
// stderr are wired to the test log so a failure-to-boot is debuggable.
func SpawnBinary(t *testing.T, ctx context.Context, bin string, env []string) (*exec.Cmd, func()) {
	t.Helper()

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(cmd.Environ(), env...)
	cmd.Dir = repoRoot
	cmd.Stdout = testLogWriter{t: t, prefix: "[bin out] "}
	cmd.Stderr = testLogWriter{t: t, prefix: "[bin err] "}

	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn binary: %v", err)
	}

	cleanup := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}

	if err := WaitForHealthz(t, "http://"+PerfHTTPAddr+"/healthz", 30*time.Second); err != nil {
		cleanup()
		t.Fatalf("healthz never came up: %v", err)
	}

	return cmd, cleanup
}

// testLogWriter routes binary stdout/stderr to the test log.
type testLogWriter struct {
	t      *testing.T
	prefix string
}

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(w.prefix + string(p))

	return len(p), nil
}

// WaitForHealthz polls until the URL returns 200 or timeout. Used after
// SpawnBinary + after a CH restart.
func WaitForHealthz(t *testing.T, url string, timeout time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx // bounded by client timeout
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("healthz at %s did not return 200 within %s", url, timeout)
}

// FireEvents posts `count` synthetic pageview events to /api/event at
// roughly `rate` events per second. Returns the number of 2xx responses
// (the rest are dropped — typically 503 from rate limit or backpressure
// during the kill).
func FireEvents(t *testing.T, ctx context.Context, hostname string, count, rate int) (sent int) {
	t.Helper()

	body := fmt.Sprintf(
		`{"hostname":%q,"pathname":"/perf","event_type":"pageview","event_name":"pageview"}`,
		hostname,
	)

	client := &http.Client{Timeout: 1 * time.Second}
	interval := time.Second / time.Duration(rate)
	t0 := time.Now()

	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			return sent
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+PerfHTTPAddr+"/api/event",
			strings.NewReader(body))
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (PerfTest/1.0) BrowserLike")
		req.Header.Set("Content-Type", "text/plain")

		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode/100 == 2 {
				sent++
			}

			_ = resp.Body.Close()
		}

		// Rate-pace: sleep until the i-th tick. Keeps total wall time
		// bounded even under brief network hiccups.
		nextTick := t0.Add(time.Duration(i+1) * interval)
		if d := time.Until(nextTick); d > 0 {
			time.Sleep(d)
		}
	}

	return sent
}

// FireEventsAsync runs FireEvents in a goroutine; the returned counter
// holds the in-flight sent count. Cancel ctx to stop early.
func FireEventsAsync(t *testing.T, ctx context.Context, hostname string, count, rate int) *atomic.Int64 {
	t.Helper()

	var sent atomic.Int64

	go func() {
		n := FireEvents(t, ctx, hostname, count, rate)
		sent.Store(int64(n))
	}()

	return &sent
}

// DockerCommand wraps `docker` for the CH outage test. Returns an error
// rather than failing the test so the caller can decide whether docker
// availability is fatal.
func DockerCommand(args ...string) error {
	cmd := exec.Command("docker", args...)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker %v: %w\n%s", args, err, out)
	}

	return nil
}
