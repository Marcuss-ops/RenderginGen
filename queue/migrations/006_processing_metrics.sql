-- processing_metrics: job-correlated timing and resource metrics.
--
-- Prometheus keeps realtime/aggregate operational metrics; this table keeps
-- the per-job numbers (render_ms, queue_wait_ms, frames_rendered, ...) that
-- answer "how long did exactly this job take?".
CREATE TABLE processing_metrics (
    id           BIGSERIAL PRIMARY KEY,
    job_id       TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    attempt_id   TEXT,
    metric_name  TEXT NOT NULL,
    metric_value DOUBLE PRECISION NOT NULL,
    unit         TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
