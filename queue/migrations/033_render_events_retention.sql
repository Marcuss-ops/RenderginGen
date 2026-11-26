-- render_events is append-only history and had no retention path: the only
-- indexes were (job_id, created_at) and id, so the age-based prune scanned the
-- whole ledger. This index makes `created_at < cutoff` an index range.
CREATE INDEX IF NOT EXISTS idx_render_events_created_at
    ON render_events (created_at);
