package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ListWorkers returns all currently registered workers.
func (c *Client) ListWorkers(ctx context.Context) ([]Worker, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/workers", nil)
	if err != nil {
		return nil, fmt.Errorf("queue list workers request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("queue list workers do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue list workers: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var workers []Worker
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		return nil, fmt.Errorf("queue list workers decode: %w", err)
	}
	return workers, nil
}

// WorkerHealth queries the worker health stats.
func (c *Client) WorkerHealth(ctx context.Context) (WorkerHealth, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/workers/health", nil)
	if err != nil {
		return WorkerHealth{}, fmt.Errorf("queue worker health request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return WorkerHealth{}, fmt.Errorf("queue worker health do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return WorkerHealth{}, fmt.Errorf("queue worker health: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var health WorkerHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return WorkerHealth{}, fmt.Errorf("queue worker health decode: %w", err)
	}
	return health, nil
}

// Retry resets a failed job back to pending state so it can be re-claimed.
func (c *Client) Retry(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/"+url.PathEscape(id)+"/retry", nil)
	if err != nil {
		return fmt.Errorf("queue retry request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue retry do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue retry: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// report POSTs a worker action (complete/fail/renew) to the job endpoint. The
// wire body carries the worker identity plus an optional data payload, matching
// the queue server's handlers.
func (c *Client) report(ctx context.Context, id, workerID, action string, payload any) error {
	body := map[string]any{"worker": workerID}
	if payload != nil {
		body["data"] = payload
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("queue %s marshal: %w", action, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/jobs/"+url.PathEscape(id)+"/"+action, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("queue %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue %s do: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("queue %s: HTTP %d: %s", action, resp.StatusCode, strings.TrimSpace(string(raw)))
		// A 409 on renew means the lease is definitively gone (expired and
		// requeued, completed, or owned by another worker). Callers use the
		// sentinel to abort immediately instead of retrying a lost lease.
		if resp.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w: %v", ErrLeaseConflict, err)
		}
		return err
	}
	return nil
}
