// Package storage implements the shared artifact store with local caching.
//
// Cache layers:
//
//	L1  VRAM cache  (handled by the Chronon/GPU layer)
//	L2  local NVMe  (LocalCacheDir)
//	L3  central object storage (Endpoint)
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Client resolves asset hashes against a central object store, keeping a
// local L2 cache on disk.
type Client struct {
	endpoint string
	cacheDir string
}

// New creates a storage client.
func New(endpoint, cacheDir string) *Client {
	return &Client{endpoint: endpoint, cacheDir: cacheDir}
}

// Get returns asset bytes for hash, using the L2 cache when possible.
func (c *Client) Get(ctx context.Context, hash string) ([]byte, error) {
	if path := c.cachePath(hash); fileExists(path) {
		return os.ReadFile(path)
	}
	// TODO: download from c.endpoint using ctx, then persist to L2.
	return nil, fmt.Errorf("asset %s not in cache and remote fetch not implemented", hash)
}

// Put stores asset bytes in the local L2 cache and (TODO) the central store.
func (c *Client) Put(hash string, data []byte) error {
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.cachePath(hash), data, 0o644)
}

func (c *Client) cachePath(hash string) string {
	prefix := hash
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(c.cacheDir, "assets", prefix, hash)
}

// Hash returns the content hash used as the cache key.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
