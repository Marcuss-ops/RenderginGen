package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSemanticAssetPathStripsQuery pins the URL hygiene fix: the logical path
// extension must derive from the parsed URL path, never the raw URL, so a
// signed CDN URL cannot contaminate the file name.
func TestSemanticAssetPathStripsQuery(t *testing.T) {
	cases := []struct {
		name string
		ref  semanticAssetRef
		want string
	}{
		{
			"jpg with token query",
			semanticAssetRef{ID: "img", SHA256: hash64("a"), URL: "https://cdn.example/image.jpg?token=abc"},
			"assets/semantic/img.jpg",
		},
		{
			"mp4 with fragment and query",
			semanticAssetRef{ID: "clip", SHA256: hash64("b"), URL: "https://cdn.example/v.mp4?Expires=1#t=5"},
			"assets/semantic/clip.mp4",
		},
		{
			"no extension falls back to media_type",
			semanticAssetRef{ID: "img2", SHA256: hash64("c"), URL: "https://cdn.example/asset", MediaType: "image/png"},
			"assets/semantic/img2.png",
		},
		{
			"assets/ prefix passes through",
			semanticAssetRef{ID: "bg", SHA256: hash64("d"), URL: "assets/backgrounds/night.mp4"},
			"assets/backgrounds/night.mp4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := semanticAssetPath(tc.ref)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("semanticAssetPath = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "?#") {
				t.Fatalf("logical path %q leaks query/fragment characters", got)
			}
		})
	}
}

// TestAssetRegistryRejectsPathCollision pins the sanitized-ID collision fix:
// two distinct asset_ids that sanitize to the same logical path must fail
// closed instead of the second asset silently overwriting the first's bytes
// at materialization.
func TestAssetRegistryRejectsPathCollision(t *testing.T) {
	r := newAssetRegistry()
	if _, err := r.Register(semanticAssetRef{ID: "img 1", SHA256: hash64("a"), URL: "https://cdn.example/a.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(semanticAssetRef{ID: "img/1", SHA256: hash64("b"), URL: "https://cdn.example/b.png"}); err == nil {
		t.Fatal("collision between 'img 1' and 'img/1' must be a compile error")
	} else if !strings.Contains(err.Error(), "collision") {
		t.Fatalf("error must name the collision, got: %v", err)
	}
}

// TestAssetRegistryRejectsHashConflict pins one-asset-id-one-hash globally.
func TestAssetRegistryRejectsHashConflict(t *testing.T) {
	r := newAssetRegistry()
	if _, err := r.Register(semanticAssetRef{ID: "x", SHA256: hash64("a"), URL: "https://cdn.example/x.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(semanticAssetRef{ID: "x", SHA256: hash64("b"), URL: "https://cdn.example/x.png"}); err == nil {
		t.Fatal("same asset_id with different SHA-256 must be a compile error")
	}
	// Idempotent re-registration of the identical pair is fine.
	path, err := r.Register(semanticAssetRef{ID: "x", SHA256: hash64("a"), URL: "https://cdn.example/x.png"})
	if err != nil || path != "assets/semantic/x.png" {
		t.Fatalf("idempotent register: path=%q err=%v", path, err)
	}
}

// TestAssetRegistryRejectsInvalidRefs keeps the fail-closed ref validation
// that previously lived duplicated in every compileSemantic section.
func TestAssetRegistryRejectsInvalidRefs(t *testing.T) {
	r := newAssetRegistry()
	for _, ref := range []semanticAssetRef{
		{ID: "", SHA256: hash64("a"), URL: "https://x/y.png"},
		{ID: "short", SHA256: "abc123", URL: "https://x/y.png"},
		{ID: "nonhex", SHA256: strings.Repeat("z", 64), URL: "https://x/y.png"},
	} {
		if _, err := r.Register(ref); err == nil {
			t.Fatalf("ref %+v must be rejected", ref)
		}
	}
}

// TestCompileSemanticCollidingItemAssetIDsRejected wires the registry
// guarantee into the full semantic compile path.
func TestCompileSemanticCollidingItemAssetIDsRejected(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"p","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,
		"items":[
			{"id":"a","template_id":"IMAGE_OVERLAY","preset_id":"image_fade_in","start_ms":0,"end_ms":1000,
			 "asset_refs":[{"asset_id":"img 1","sha256":"` + hash64("a") + `","url":"https://cdn.example/first.png","media_type":"image/png"}]},
			{"id":"b","template_id":"IMAGE_OVERLAY","preset_id":"image_fade_in","start_ms":1000,"end_ms":2000,
			 "asset_refs":[{"asset_id":"img/1","sha256":"` + hash64("b") + `","url":"https://cdn.example/second.png","media_type":"image/png"}]}
		]
	}`)
	if _, _, _, err := CompileIfSemantic(raw); err == nil {
		t.Fatal("semantic plan with colliding sanitized asset ids must be rejected")
	} else if !strings.Contains(err.Error(), "collision") {
		t.Fatalf("error must name the collision, got: %v", err)
	}
}

// TestCompileSemanticQueryURLAssetCleanPath verifies the whole compiler emits
// clean logical paths for signed URLs (visible in the returned manifest).
func TestCompileSemanticQueryURLAssetCleanPath(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"p","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,
		"items":[
			{"id":"a","template_id":"IMAGE_OVERLAY","preset_id":"image_fade_in","start_ms":0,"end_ms":1000,
			 "asset_refs":[{"asset_id":"photo","sha256":"` + hash64("a") + `","url":"https://cdn.example/photo.png?sig=zzz","media_type":"image/png"}]}
		]
	}`)
	_, assets, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets = %+v, want 1", assets)
	}
	if assets[0].LogicalPath != "assets/semantic/photo.png" {
		t.Fatalf("logical path = %q, want assets/semantic/photo.png", assets[0].LogicalPath)
	}
	var probe struct {
		Layers []struct {
			Asset string `json:"asset"`
		} `json:"layers"`
	}
	// Decode from a fresh compile to also verify the layer reference.
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(compiled, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Layers[0].Asset != "assets/semantic/photo.png" {
		t.Fatalf("layer asset = %q, want clean path", probe.Layers[0].Asset)
	}
}

func hash64(seed string) string {
	// Deterministic 64-char lowercase hex fixture; content-addressing is not
	// exercised here, only the format contract.
	out := make([]byte, 0, 64)
	for len(out) < 64 {
		for _, c := range seed + "0123456789abcdef" {
			out = append(out, byte(c))
			if len(out) == 64 {
				break
			}
		}
	}
	return string(out)
}
