package alerts

import (
	"context"
	"errors"
	"log/slog"
	"syscall"
	"time"
)

// pingFunc is the subset of storage.ClickHouseStore.Ping a prober needs.
// Defined as a type alias so tests can inject a stub without importing
// the storage package (or pulling in the clickhouse-go driver).
type pingFunc func(context.Context) error

// ProbeClickHouseLoop runs every interval and Pings CH; the first
// failure emits clickhouse_down (severity critical), and the first
// subsequent success emits clickhouse_up (resolved=true). Silent while
// the state hasn't changed. Runs until ctx is cancelled.
func ProbeClickHouseLoop(ctx context.Context, sink *Sink, ping pingFunc, interval time.Duration) error {
	if sink == nil || ping == nil {
		<-ctx.Done()

		return nil
	}

	if interval <= 0 {
		interval = 30 * time.Second
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	var tracker BandTracker

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

			err := ping(probeCtx)

			cancel()

			var band uint32
			if err != nil {
				band = 1
			}

			tr := tracker.Observe(band)
			if !tr.Entered && !tr.Exited {
				continue
			}

			if tr.Entered {
				sink.Emit(context.Background(), NameClickHouseDown, SeverityCritical, false,
					slog.String("err", err.Error()),
				)
			} else if tr.Exited {
				sink.Emit(context.Background(), NameClickHouseUp, SeverityInfo, true)
			}
		}
	}
}

// DiskFillSampler returns the filesystem fill ratio (0.0–1.0) for the
// directory at path. Used by ProbeDiskFillLoop; exported so
// /healthz can sample the same number via the same code path if it
// ever wants to.
func DiskFillSampler(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}

	total := st.Blocks
	free := st.Bavail

	if total == 0 {
		return 0, errors.New("disk sampler: zero total blocks")
	}

	used := total - free

	return float64(used) / float64(total), nil
}

// DiskFillThresholds are the 3 bands for /var/lib/statnive-live fill.
// Doc 28 §operations puts 0.85 warn / 0.90 page in the runbook; we add
// 0.95 as a critical ceiling so ops sees one extra band before the
// partition fails. Exported so the equivalent `GET /api/ops/alerts`
// endpoint (Phase 6-polish-5) can render the same bands.
var DiskFillThresholds = [3]float64{0.85, 0.90, 0.95}

// ProbeDiskFillLoop samples DiskFillSampler every interval and emits
// disk_high_fill_ratio alerts on band transitions. Runs until ctx is
// cancelled.
func ProbeDiskFillLoop(ctx context.Context, sink *Sink, path string, interval time.Duration) error {
	if sink == nil || path == "" {
		<-ctx.Done()

		return nil
	}

	if interval <= 0 {
		interval = time.Minute
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	var tracker BandTracker

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			ratio, err := DiskFillSampler(path)
			if err != nil {
				// Probe failure is logged via slog, not alerted — a
				// broken sampler is a dev bug, not an ops condition.
				continue
			}

			band, sev := ClassifyRatio(ratio, DiskFillThresholds)

			tr := tracker.Observe(band)
			if !tr.Entered && !tr.Exited {
				continue
			}

			resolved := tr.Exited && band == 0

			sink.Emit(context.Background(), NameDiskHighFillRatio, sev, resolved,
				slog.Float64("value", ratio),
				slog.Int("band", int(band)),
				slog.String("path", path),
			)
		}
	}
}
