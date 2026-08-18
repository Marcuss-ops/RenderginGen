package artifactdb

import (
	"context"
	"sync"
)

// Recorder persists an ArtifactRecord. The processor owns the record and
// calls Record after the object store accepted the bytes; a nil Recorder (the
// default) disables the artifact ledger without failing the job.
type Recorder interface {
	Record(ctx context.Context, rec ArtifactRecord) error
}

// DriveUpdater is the optional extension a Recorder may implement so a
// publication retry (Drive failed, job stays rendered) updates the recorded
// drive_upload_us metric WITHOUT re-rendering or re-recording the artifact.
// Plan section "job state": RENDERED -> PUBLISH_RETRY -> PUBLISHED, never a
// Chronon re-render.
type DriveUpdater interface {
	UpdateDrive(ctx context.Context, jobID string, driveUploadUS int64) error
}

// MemoryRecorder is a deterministic in-memory Recorder for tests and local
// runs. Records are kept keyed by job ID; repeated records for the same job
// replace the previous one (idempotent retries).
type MemoryRecorder struct {
	mu      sync.Mutex
	records map[string]ArtifactRecord
}

// NewMemory creates an empty MemoryRecorder.
func NewMemory() *MemoryRecorder {
	return &MemoryRecorder{records: make(map[string]ArtifactRecord)}
}

func (m *MemoryRecorder) Record(_ context.Context, rec ArtifactRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[rec.JobID] = rec
	return nil
}

// Get returns the record for a job, and false when absent.
func (m *MemoryRecorder) Get(jobID string) (ArtifactRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[jobID]
	return rec, ok
}

// Len returns the number of distinct recorded jobs.
func (m *MemoryRecorder) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

// UpdateDrive rewrites only the drive_upload_us metric of an existing record.
// It is idempotent: the record may or may not exist yet (publication retry
// after a crash), and the update never touches the artifact identity.
func (m *MemoryRecorder) UpdateDrive(_ context.Context, jobID string, driveUploadUS int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[jobID]
	if !ok {
		return nil
	}
	rec.DriveUploadUS = driveUploadUS
	m.records[jobID] = rec
	return nil
}
