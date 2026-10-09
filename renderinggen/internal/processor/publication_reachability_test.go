package processor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// TestDeclaredPublicationPolicyIsReachableFromTheWire pins the public wire
// field and verifies an explicit request survives JSON transport into the same
// resolver used by Processor.Publish. Omission remains safely store-only.
func TestDeclaredPublicationPolicyIsReachableFromTheWire(t *testing.T) {
	field, ok := reflect.TypeOf(client.Job{}).FieldByName("PublicationPolicy")
	if !ok || strings.Split(field.Tag.Get("json"), ",")[0] != "publication_policy" {
		t.Fatal("queue job must expose publication_policy on the wire")
	}

	job := client.Job{PublicationPolicy: string(PublicationObjectStoreAndDrive)}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var decoded client.Job
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePublicationPolicy(decoded.PublicationPolicy, client.JobTypeRenderSegment); got != PublicationObjectStoreAndDrive {
		t.Fatalf("wire publication policy resolves to %q, want %q", got, PublicationObjectStoreAndDrive)
	}

	var omitted client.Job
	if got := ResolvePublicationPolicy(omitted.PublicationPolicy, client.JobTypeRenderSegment); got != PublicationObjectStoreOnly {
		t.Fatalf("omitted policy resolves to %q, want safe default %q", got, PublicationObjectStoreOnly)
	}
}
