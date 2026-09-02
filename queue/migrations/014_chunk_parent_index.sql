-- Accelerates deterministic parent-job chunk aggregation.
CREATE INDEX IF NOT EXISTS idx_render_jobs_parent_chunk
    ON render_jobs(parent_job_id, chunk_index)
    WHERE parent_job_id IS NOT NULL;
