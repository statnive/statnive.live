// should-not-trigger: emit path with all four X-Statnive-* oracle headers.
// Every call site below MUST NOT be flagged by oracle-fields-required-*.
package shouldnottrigger

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"time"
)

func emitGood(ctx context.Context, client *http.Client, target, runID string, nodeID uint16, seq uint64, body []byte) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		target+"/api/event", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Statnive-Test-Run-Id", runID)
	req.Header.Set("X-Statnive-Generator-Node-Id", strconv.FormatUint(uint64(nodeID), 10))
	req.Header.Set("X-Statnive-Test-Generator-Seq", strconv.FormatUint(seq, 10))
	req.Header.Set("X-Statnive-Send-Ts", strconv.FormatInt(time.Now().UnixMilli(), 10))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
