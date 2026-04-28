// should-trigger: emit path missing the four X-Statnive-* oracle headers.
// Every call site below MUST be flagged by oracle-fields-required-*.
package shouldtrigger

import (
	"bytes"
	"context"
	"net/http"
)

func emitBad(ctx context.Context, client *http.Client, target string, body []byte) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		target+"/api/event", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
