package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

const preparedRenderPackageSchemaV1 = "renderinggen.prepared-render-package.v1"

// PreparedRenderPackageV1 is the durable, content-addressed output of the
// prepare-only worker job. It contains no worker-local paths or fetch URLs.
// Chunk jobs refer to the immutable object by its storage hash and carry only
// their own frame range plus the same source plan and asset manifest.
type PreparedRenderPackageV1 struct {
	SchemaVersion       string                  `json:"schema_version"`
	PlanSHA256          string                  `json:"plan_sha256"`
	AssetManifestSHA256 string                  `json:"asset_manifest_sha256"`
	CompilerVersion     string                  `json:"compiler_version"`
	ProfileID           string                  `json:"profile_id"`
	Plan                json.RawMessage         `json:"compiled_plan"`
	Assets              []queue.AssetRef        `json:"assets"`
	Overlay             overlay.PreparedPackage `json:"overlay_package"`
	Stats               overlay.Stats           `json:"stats"`
}

// PrepareRenderPackage compiles and materializes a complete immutable render
// package without invoking Chronon. The queue artifact hash is the package ID;
// workers verify that hash and all request identities before reuse.
func (p *Processor) PrepareRenderPackage(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	if job == nil || job.JobType != queue.JobTypeRenderPrepare {
		return queue.Artifact{}, fmt.Errorf("processor: render.prepare job type is required")
	}
	if job.PreparedRenderPackage != nil {
		return queue.Artifact{}, fmt.Errorf("processor: render.prepare cannot itself reference a prepared package")
	}
	prepared, err := p.PrepareJob(ctx, job)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: prepare render package: %w", err)
	}
	defer p.cleanupWorkspace(prepared.Workspace, job.ID)

	planBytes, err := prepared.Plan.Marshal()
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: encode prepared render plan: %w", err)
	}
	if err := prepared.PreparedOverlay.Validate(); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: validate prepared overlay package: %w", err)
	}
	assets := make([]queue.AssetRef, len(prepared.ResolvedAssets))
	for i, asset := range prepared.ResolvedAssets {
		assets[i] = queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath}
	}
	profileID := planMetadataOf(prepared.Plan).ProfileID
	packageV1 := PreparedRenderPackageV1{
		SchemaVersion:       preparedRenderPackageSchemaV1,
		PlanSHA256:          preparedRenderPlanSHA256(job.RenderPlan),
		AssetManifestSHA256: preparedRenderAssetManifestSHA(job.Assets),
		CompilerVersion:     p.chrononVersion,
		ProfileID:           profileID,
		Plan:                planBytes,
		Assets:              assets,
		Overlay:             prepared.PreparedOverlay,
		Stats:               prepared.Stats,
	}
	if err := packageV1.Validate(); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: prepared render package contract: %w", err)
	}
	packageBytes, err := json.Marshal(packageV1)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: encode prepared render package: %w", err)
	}
	packageID := storage.Hash(packageBytes)
	if err := p.store.Put(ctx, packageID, packageBytes); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: store prepared render package: %w", err)
	}
	return queue.Artifact{
		Kind: "prepared_render_package", StorageKey: packageID, ArtifactURL: p.artifactURL(packageID),
		ArtifactHash: packageID, ContentType: "application/json", SizeBytes: int64(len(packageBytes)),
		ProfileID: profileID, Backend: p.backend, ChrononVersion: p.chrononVersion,
		Metrics: map[string]float64{},
	}, nil
}

// Validate rejects a stale, incomplete or internally inconsistent package.
// The object-store content hash is checked separately at fetch time.
func (p PreparedRenderPackageV1) Validate() error {
	if p.SchemaVersion != preparedRenderPackageSchemaV1 {
		return fmt.Errorf("schema_version %q is unsupported", p.SchemaVersion)
	}
	if !isSHA256(p.PlanSHA256) || !isSHA256(p.AssetManifestSHA256) {
		return fmt.Errorf("plan and asset manifest identities must be lowercase SHA-256 values")
	}
	if strings.TrimSpace(p.CompilerVersion) == "" {
		return fmt.Errorf("compiler_version is required")
	}
	if len(p.Plan) == 0 || !json.Valid(p.Plan) {
		return fmt.Errorf("compiled_plan must be valid JSON")
	}
	for i, asset := range p.Assets {
		if !isSHA256(asset.Hash) || strings.TrimSpace(asset.LogicalPath) == "" || asset.SourceURL != "" {
			return fmt.Errorf("assets[%d] must contain a content hash and logical path, without a fetch URL", i)
		}
	}
	if err := p.Overlay.Validate(); err != nil {
		return fmt.Errorf("overlay_package: %w", err)
	}
	return nil
}

func (p *Processor) loadPreparedRenderPackage(ctx context.Context, job *queue.Job) (PreparedRenderPackageV1, error) {
	if job == nil || job.PreparedRenderPackage == nil {
		return PreparedRenderPackageV1{}, fmt.Errorf("prepared render package reference is required")
	}
	ref := job.PreparedRenderPackage
	if err := ref.Validate(); err != nil {
		return PreparedRenderPackageV1{}, err
	}
	raw, err := p.store.Get(ctx, ref.PackageID)
	if err != nil {
		return PreparedRenderPackageV1{}, fmt.Errorf("fetch package %s: %w", ref.PackageID, err)
	}
	if storage.Hash(raw) != ref.PackageID {
		return PreparedRenderPackageV1{}, fmt.Errorf("package %s content hash mismatch", ref.PackageID)
	}
	var pkg PreparedRenderPackageV1
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pkg); err != nil {
		return PreparedRenderPackageV1{}, fmt.Errorf("decode package %s: %w", ref.PackageID, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return PreparedRenderPackageV1{}, fmt.Errorf("decode package %s: trailing JSON value", ref.PackageID)
		}
		return PreparedRenderPackageV1{}, fmt.Errorf("decode package %s: trailing data: %w", ref.PackageID, err)
	}
	if err := pkg.Validate(); err != nil {
		return PreparedRenderPackageV1{}, fmt.Errorf("validate package %s: %w", ref.PackageID, err)
	}
	if pkg.PlanSHA256 != ref.PlanSHA256 || pkg.AssetManifestSHA256 != ref.AssetManifestSHA ||
		pkg.CompilerVersion != ref.CompilerVersion || pkg.ProfileID != ref.ProfileID {
		return PreparedRenderPackageV1{}, fmt.Errorf("package %s does not match the queue reference", ref.PackageID)
	}
	if preparedRenderPlanSHA256(job.RenderPlan) != pkg.PlanSHA256 {
		return PreparedRenderPackageV1{}, fmt.Errorf("package %s plan identity does not match render request", ref.PackageID)
	}
	if preparedRenderAssetManifestSHA(job.Assets) != pkg.AssetManifestSHA256 {
		return PreparedRenderPackageV1{}, fmt.Errorf("package %s asset manifest does not match render request", ref.PackageID)
	}
	if pkg.CompilerVersion != p.chrononVersion {
		return PreparedRenderPackageV1{}, fmt.Errorf("package %s compiler %q does not match worker %q", ref.PackageID, pkg.CompilerVersion, p.chrononVersion)
	}
	return pkg, nil
}

func isSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func preparedRenderPlanSHA256(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return ""
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return ""
	}
	return storage.Hash(canonical)
}

type preparedRenderAssetIdentity struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

func preparedRenderAssetManifestSHA(assets []queue.AssetRef) string {
	entries := make([]preparedRenderAssetIdentity, 0, len(assets))
	for _, asset := range assets {
		entries = append(entries, preparedRenderAssetIdentity{
			Hash: strings.ToLower(strings.TrimSpace(asset.Hash)), LogicalPath: strings.TrimSpace(asset.LogicalPath),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Hash != entries[j].Hash {
			return entries[i].Hash < entries[j].Hash
		}
		return entries[i].LogicalPath < entries[j].LogicalPath
	})
	encoded, _ := json.Marshal(entries)
	return storage.Hash(encoded)
}
