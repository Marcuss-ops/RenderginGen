package processor

// publication.go owns the canonical external-publication policy: the single
// place that answers "who delivers this rendered artifact to its external
// destination (Google Drive)?" for the jobs the worker serves.
//
// One semantic:
//
//	The worker's job contract ends when the artifact is durable in the
//	object store. External delivery of a queue-served render segment is the
//	job SUBMITTER's authority — PipelineGen master submits clip render
//	segments and publishes each clip into its destination Drive folder
//	itself. If the worker also uploaded every segment to its configured
//	default Drive folder, every master-routed clip would be uploaded twice,
//	and the duplicate upload would back-pressure the GPU lanes through the
//	bounded post pool.
//
// So config (drive.enabled) stays a pure CAPABILITY constraint — "the worker
// holds Drive credentials" — and never decides a job's intent. Intent is
// decided here, once, by the job's declared policy (a future submitter that
// wants the worker to deliver asks for object_store_and_drive) defaulting to
// store-only. The publisher (Processor.Publish) is the ONLY consumer of the
// resolved policy.
//
// WHO RENDERS?      Chronon (via the worker pipeline).
// WHO STORES?      RenderingGen worker / object store.
// WHO DELIVERS TO DRIVE? The job submitter (master), except when a job
//                        explicitly declares the worker as its publisher.

// PublicationPolicy is the canonical external-delivery policy for a rendered
// artifact.
type PublicationPolicy string

const (
	// PublicationObjectStoreOnly resolves every queue-served render segment:
	// RenderingGen makes the artifact durable in the object store and the
	// job completes; Drive delivery is the submitter's (master's) seam.
	PublicationObjectStoreOnly PublicationPolicy = "object_store_only"

	// PublicationObjectStoreAndDrive additionally uploads the durable
	// artifact to the worker's configured Drive destination before the job
	// completes. A submitter requests this when the worker IS the delivery
	// authority (standalone RenderingGen deployments with no publisher of
	// their own). The worker's Drive capability (config drive.enabled) is
	// still required for the upload to be possible.
	PublicationObjectStoreAndDrive PublicationPolicy = "object_store_and_drive"
)

// ParsePublicationPolicy validates a declared policy. "" and unknown values
// resolve to the store-only default — never to a guessed Drive upload.
func ParsePublicationPolicy(declared string) (PublicationPolicy, bool) {
	switch PublicationPolicy(declared) {
	case PublicationObjectStoreAndDrive:
		return PublicationObjectStoreAndDrive, true
	case PublicationObjectStoreOnly:
		return PublicationObjectStoreOnly, true
	default:
		return PublicationObjectStoreOnly, false
	}
}

// ResolvePublicationPolicy is the SINGLE canonical resolver. A job's declared
// policy (the requested publication policy, carried by the submitter) wins;
// an undeclared queue render job defaults to object-store-only, because the
// queue contract is "render a segment", not "deliver a segment". Changing
// who delivers for a job type means changing this one function — never a
// scattered `if job.JobType == ...` in the pools.
func ResolvePublicationPolicy(declared, jobType string) PublicationPolicy {
	if policy, ok := ParsePublicationPolicy(declared); ok {
		return policy
	}
	// The table currently defaults every queue render type to store-only
	// (jobType is the resolver's second input so a future render-and-deliver
	// job type is added here as its own case, in this one function).
	return PublicationObjectStoreOnly
}
