package processor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// TestDeclaredPublicationPolicyIsNotYetOnTheWire makes an invisible dead branch
// visible and self-enforcing.
//
// ResolvePublicationPolicy takes two inputs: the declared policy and the job
// type. Today the ONLY production caller passes declared="" (publish_drive.go),
// because the wire job contract carries no publication field: every queue-served
// render segment resolves to object-store-only by construction, and the branch
// that would honour a declared `object_store_and_drive` is reachable only from
// tests.
//
// That is a real gap — the resolver documents a capability no producer can ask
// for. Rather than leaving it implicit, this test fails as soon as the wire
// contract grows a publication field, with the instruction to wire it into
// Publish in the same change. Keeping "" at the call site silently (the shape
// that let the gap persist) stops compiling the moment the field exists.
func TestDeclaredPublicationPolicyIsNotYetOnTheWire(t *testing.T) {
	typ := reflect.TypeOf(client.Job{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if strings.Contains(strings.ToLower(field.Name), "publication") ||
			strings.Contains(strings.ToLower(jsonName), "publication") {
			t.Fatalf("client.Job now carries %s (%s): pass it to ResolvePublicationPolicy at the Publish call site "+
				"(publish_drive.go) instead of the hardcoded \"\", otherwise the declared policy stays unreachable",
				field.Name, jsonName)
		}
	}

	// The resolver must keep honouring a declared policy: the gap is the missing
	// wire field, not a lost rule.
	if got := ResolvePublicationPolicy(string(PublicationObjectStoreAndDrive), client.JobTypeRenderSegment); got != PublicationObjectStoreAndDrive {
		t.Fatalf("a declared policy must still win in the resolver: got %q", got)
	}
	if got := ResolvePublicationPolicy("", client.JobTypeRenderSegment); got != PublicationObjectStoreOnly {
		t.Fatalf("an undeclared queue render must stay store-only: got %q", got)
	}
}
