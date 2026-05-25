package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// ChangeRow is a single row from the npm _changes feed.
type ChangeRow struct {
	Seq     uint64 `json:"seq"`
	ID      string `json:"id"` // package name
	Deleted bool   `json:"deleted"`
}

// changesResponse is the JSON shape returned by the longpoll endpoint.
type changesResponse struct {
	Results []json.RawMessage `json:"results"`
	LastSeq interface{}       `json:"last_seq"` // can be number or string depending on registry
}

// rawChange is the minimal fields we need from each change row.
type rawChange struct {
	Seq     interface{} `json:"seq"` // can be number or string
	ID      string      `json:"id"`
	Deleted bool        `json:"deleted"`
}

// parseSeq parses a seq value that may be a number or a string.
func parseSeq(v interface{}) (uint64, error) {
	switch t := v.(type) {
	case float64:
		return uint64(t), nil
	case string:
		return strconv.ParseUint(t, 10, 64)
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0, err
		}
		return uint64(n), nil
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected seq type %T: %v", v, v)
	}
}

// Poll opens a request to <baseURL>/_changes?since=<since>&limit=<n>
// and yields each change row to onRow. Returns the last_seq from the response.
// Caller should loop with the returned seq as the next since value.
// On HTTP error, returns a wrapped error; caller handles backoff.
//
// Note: replicate.npmjs.com does not support feed=longpoll or feed=continuous;
// we use plain polling with client-side interval management.
func Poll(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	userAgent string,
	since uint64,
	limit int,
	onRow func(ChangeRow) error,
) (uint64, error) {
	url := fmt.Sprintf(
		"%s/_changes?since=%d&limit=%d",
		baseURL, since, limit,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return since, fmt.Errorf("registry: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return since, fmt.Errorf("registry: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return since, fmt.Errorf("registry: unexpected status %d: %s", resp.StatusCode, body)
	}

	// Decode the response body with json.Decoder to avoid reading all bytes into memory.
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber() // preserve exact numeric representation

	var cr changesResponse
	if err := dec.Decode(&cr); err != nil {
		return since, fmt.Errorf("registry: decode response: %w", err)
	}

	// Parse and yield rows.
	for _, raw := range cr.Results {
		var rc rawChange
		if err := json.Unmarshal(raw, &rc); err != nil {
			// Skip malformed rows; don't abort the batch.
			continue
		}
		seq, err := parseSeq(rc.Seq)
		if err != nil {
			// If we can't parse the seq, use last seen.
			continue
		}
		row := ChangeRow{Seq: seq, ID: rc.ID, Deleted: rc.Deleted}
		if err := onRow(row); err != nil {
			return seq, err
		}
	}

	// Parse last_seq.
	lastSeq, err := parseSeq(cr.LastSeq)
	if err != nil {
		// If unparseable, return the since value unchanged.
		return since, nil
	}
	if lastSeq == 0 {
		return since, nil
	}
	return lastSeq, nil
}
