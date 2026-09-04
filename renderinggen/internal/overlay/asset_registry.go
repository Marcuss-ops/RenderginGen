package overlay

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// asset_registry.go is the single owner of semantic asset registration:
// asset_id + sha256 + url → content-addressed logical path. Background,
// source, items, subtitles and watermark fonts all register through this one
// resolver, which enforces globally:
//
//   - one asset_id ⇒ exactly one SHA-256 (conflicts are compile errors);
//   - one logical_path ⇒ exactly one asset identity (sanitized-ID collisions
//     such as "img 1" vs "img/1" are compile errors instead of the second
//     asset silently overwriting the first's bytes at materialization);
//   - extensions derive from the PARSED URL path, never the raw URL, so a
//     query string (?token=…) cannot contaminate the logical file name.
//
// This replaces the scattered per-section ref validation that previously
// lived in every background/item/subtitle/watermark branch of compileSemantic.
type assetRegistry struct {
	byID   map[string]registeredAsset // asset_id → registration
	byPath map[string]registeredAsset // logical_path → registration
}

type registeredAsset struct {
	id          string
	sha256      string
	logicalPath string
}

var safeAssetID = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func newAssetRegistry() *assetRegistry {
	return &assetRegistry{byID: map[string]registeredAsset{}, byPath: map[string]registeredAsset{}}
}

// Register resolves one semantic asset ref to its content-addressed logical
// path. Re-registering the identical (id, sha256) pair is idempotent; any
// conflicting identity — same id with a different hash, or a different id
// collapsing onto the same logical path — fails closed.
func (r *assetRegistry) Register(ref semanticAssetRef) (string, error) {
	if strings.TrimSpace(ref.ID) == "" || len(ref.SHA256) != 64 || strings.Trim(ref.SHA256, "0123456789abcdefABCDEF") != "" {
		return "", fmt.Errorf("overlay: invalid asset ref %q (asset_id required, sha256 must be 64 hex chars)", ref.ID)
	}
	hash := strings.ToLower(ref.SHA256)
	if existing, ok := r.byID[ref.ID]; ok {
		if existing.sha256 != hash {
			return "", fmt.Errorf("overlay: asset_id %q is associated with multiple SHA-256 values", ref.ID)
		}
		return existing.logicalPath, nil
	}
	path, err := semanticAssetPath(ref)
	if err != nil {
		return "", err
	}
	if existing, ok := r.byPath[path]; ok {
		return "", fmt.Errorf("overlay: asset_id %q resolves to logical path %s already owned by asset_id %q (sanitized-id collision — rename one of the assets)", ref.ID, path, existing.id)
	}
	reg := registeredAsset{id: ref.ID, sha256: hash, logicalPath: path}
	r.byID[ref.ID] = reg
	r.byPath[path] = reg
	return path, nil
}

// Path returns the registered logical path for an asset_id, empty when the id
// was never registered.
func (r *assetRegistry) Path(id string) string {
	if reg, ok := r.byID[id]; ok {
		return reg.logicalPath
	}
	return ""
}

// Assets returns the content-addressed manifest in stable (sorted) order so
// prepared-plan fingerprints remain reproducible.
func (r *assetRegistry) Assets() []Asset {
	assets := make([]Asset, 0, len(r.byID))
	for _, reg := range r.byID {
		assets = append(assets, Asset{Hash: reg.sha256, LogicalPath: reg.logicalPath})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].LogicalPath < assets[j].LogicalPath })
	return assets
}

// semanticAssetPath derives the workspace-relative logical path for one
// semantic ref. The extension comes from the parsed URL path (query strings
// stripped by url.Parse), falling back to the declared media_type.
func semanticAssetPath(ref semanticAssetRef) (string, error) {
	if strings.HasPrefix(ref.URL, "assets/") {
		return filepath.ToSlash(ref.URL), nil
	}
	assetPath := ref.URL
	if parsed, err := url.Parse(ref.URL); err == nil && parsed.Path != "" {
		assetPath = parsed.Path
	}
	ext := filepath.Ext(assetPath)
	if ext == "" {
		switch strings.ToLower(ref.MediaType) {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "video/mp4":
			ext = ".mp4"
		case "font/ttf":
			ext = ".ttf"
		}
	}
	id := safeAssetID.ReplaceAllString(ref.ID, "_")
	return "assets/semantic/" + id + ext, nil
}
