package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Depth returns a snapshot of the queue state.
func (c *Client) Depth(ctx context.Context) (Stats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/depth", nil)
	if err != nil {
		return Stats{}, fmt.Errorf("queue depth request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Stats{}, fmt.Errorf("queue depth do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Stats{}, fmt.Errorf("queue depth: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var stats Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return Stats{}, fmt.Errorf("queue depth decode: %w", err)
	}
	return stats, nil
}

// Health reports whether the queue service is reachable and healthy.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("queue health request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue health do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue health: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// Wait polls Get until the job reaches a terminal state. It retains the
// polling API for compatibility but uses exponential backoff, avoiding a
// fixed multi-second latency for short jobs while limiting request pressure.
func (c *Client) Wait(ctx context.Context, id string, interval time.Duration) (Job, error) {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	backoff := interval
	for {
		job, err := c.Get(ctx, id)
		if err == nil && (job.State == StateCompleted || job.State == StateFailed) {
			return job, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Job{}, err
		}
		select {
		case <-ctx.Done():
			return Job{}, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
	}
}
