package overlay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const editorialCompositionSchemaV1 = "editorial_composition.v1"

type editorialCompositionWire struct {
	SchemaVersion string                       `json:"schema_version"`
	CompositionID string                       `json:"composition_id"`
	SceneID       string                       `json:"scene_id"`
	Window        editorialCompositionWindow   `json:"window"`
	Members       []editorialCompositionMember `json:"members"`
}

type editorialCompositionWindow struct {
	StartUS    int64 `json:"start_us"`
	DurationUS int64 `json:"duration_us"`
}

type editorialCompositionMember struct {
	MemberID   string                      `json:"member_id"`
	Kind       string                      `json:"kind"`
	StartUS    int64                       `json:"start_us"`
	DurationUS int64                       `json:"duration_us"`
	Reveal     string                      `json:"reveal"`
	Points     []editorialCompositionPoint `json:"points"`
}

type editorialCompositionPoint struct {
	Text       string `json:"text"`
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
}

// expandEditorialCompositions lowers planner-certified progressive summaries
// into ordinary phrase items and a progressive_reveal group. From that point
// onward the established semantic compiler owns timing, motion selection,
// layout and Chronon layer generation.
func expandEditorialCompositions(src *semanticPlan) error {
	if src == nil || len(src.EditorialCompositions) == 0 {
		return nil
	}
	if src.FPSNum <= 0 || src.FPSDen <= 0 {
		return fmt.Errorf("overlay: editorial_compositions require a positive frame rate")
	}
	itemIDs := make(map[string]struct{}, len(src.Items))
	for _, item := range src.Items {
		itemIDs[item.ID] = struct{}{}
	}
	groupIDs := make(map[string]struct{}, len(src.AnimationGroups))
	for _, group := range src.AnimationGroups {
		groupIDs[group.ID] = struct{}{}
	}
	for compositionIndex, raw := range src.EditorialCompositions {
		var composition editorialCompositionWire
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&composition); err != nil {
			return fmt.Errorf("overlay: editorial_compositions[%d] decode: %w", compositionIndex, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("overlay: editorial_compositions[%d] must contain one JSON value", compositionIndex)
		}
		if err := validateEditorialSummary(composition); err != nil {
			return fmt.Errorf("overlay: editorial_compositions[%d]: %w", compositionIndex, err)
		}
		groupID, itemIDsForGroup, members, startMS, endMS, err := lowerEditorialSummary(src, composition)
		if err != nil {
			return fmt.Errorf("overlay: editorial_compositions[%d]: %w", compositionIndex, err)
		}
		if _, duplicate := groupIDs[groupID]; duplicate {
			return fmt.Errorf("overlay: editorial_compositions[%d] duplicates animation group %q", compositionIndex, groupID)
		}
		groupIDs[groupID] = struct{}{}
		for _, item := range members {
			if _, duplicate := itemIDs[item.ID]; duplicate {
				return fmt.Errorf("overlay: editorial_compositions[%d] generated duplicate item id %q", compositionIndex, item.ID)
			}
			itemIDs[item.ID] = struct{}{}
			src.Items = append(src.Items, item)
		}
		groupMembers := make([]semanticAnimationGroupMember, len(members))
		for i, item := range members {
			groupMembers[i] = semanticAnimationGroupMember{
				ItemID:   itemIDsForGroup[i],
				Role:     AnimationGroupRolePhrase,
				OffsetMS: int64Pointer(item.StartMS - startMS),
			}
		}
		src.AnimationGroups = append(src.AnimationGroups, semanticAnimationGroup{
			ID:             groupID,
			StartMS:        startMS,
			EndMS:          endMS,
			ChoreographyID: AnimationGroupChoreographyProgressiveReveal,
			Members:        groupMembers,
		})
	}
	return nil
}

func validateEditorialSummary(composition editorialCompositionWire) error {
	if composition.SchemaVersion != editorialCompositionSchemaV1 ||
		strings.TrimSpace(composition.CompositionID) == "" || strings.TrimSpace(composition.SceneID) == "" {
		return fmt.Errorf("requires schema_version %q, composition_id, and scene_id", editorialCompositionSchemaV1)
	}
	if composition.Window.StartUS < 0 || composition.Window.DurationUS <= 0 ||
		composition.Window.StartUS > maxInt64()-composition.Window.DurationUS || len(composition.Members) != 1 {
		return fmt.Errorf("requires one member inside a positive composition window")
	}
	member := composition.Members[0]
	if member.Kind != "summary" || member.Reveal != "progressive" || strings.TrimSpace(member.MemberID) == "" ||
		member.DurationUS <= 0 || len(member.Points) < 2 || len(member.Points) > 3 {
		return fmt.Errorf("currently accepts one progressive summary member with 2..3 points")
	}
	if member.StartUS < 0 || member.DurationUS <= 0 || member.StartUS > maxInt64()-member.DurationUS {
		return fmt.Errorf("summary member has invalid timing")
	}
	windowEnd := composition.Window.StartUS + composition.Window.DurationUS
	memberEnd := member.StartUS + member.DurationUS
	if member.StartUS < composition.Window.StartUS || memberEnd > windowEnd || member.StartUS < 0 {
		return fmt.Errorf("summary member lies outside its scene window")
	}
	previousStart := int64(-1)
	previousEnd := int64(-1)
	for index, point := range member.Points {
		if point.StartUS < 0 || point.DurationUS <= 0 || point.StartUS > maxInt64()-point.DurationUS {
			return fmt.Errorf("summary point %d has invalid timing", index)
		}
		end := point.StartUS + point.DurationUS
		if strings.TrimSpace(point.Text) == "" || point.DurationUS <= 0 || point.StartUS < member.StartUS || end > memberEnd ||
			(index > 0 && (point.StartUS <= previousStart || point.StartUS < previousEnd)) {
			return fmt.Errorf("summary point %d has empty text, invalid timing, or is not strictly ordered", index)
		}
		previousStart, previousEnd = point.StartUS, end
	}
	if member.StartUS != member.Points[0].StartUS || memberEnd != previousEnd {
		return fmt.Errorf("summary member window must exactly cover its first and last source points")
	}
	return nil
}

func lowerEditorialSummary(src *semanticPlan, composition editorialCompositionWire) (string, []string, []semanticItem, int64, int64, error) {
	member := composition.Members[0]
	memberIDs := make([]string, len(member.Points))
	items := make([]semanticItem, len(member.Points))
	groupID := editorialStableID("edg", src.PlanID, composition.CompositionID)
	startMS := floorMicrosecondsToMilliseconds(member.Points[0].StartUS)
	last := member.Points[len(member.Points)-1]
	endMS := ceilMicrosecondsToMilliseconds(last.StartUS + last.DurationUS)
	if startMS < 0 || endMS <= startMS {
		return "", nil, nil, 0, 0, fmt.Errorf("summary timing is outside the millisecond timeline")
	}
	for index, point := range member.Points {
		pointStartMS := floorMicrosecondsToMilliseconds(point.StartUS)
		pointEndMS := ceilMicrosecondsToMilliseconds(point.StartUS + point.DurationUS)
		if pointEndMS <= pointStartMS || pointStartMS < startMS || pointEndMS > endMS {
			return "", nil, nil, 0, 0, fmt.Errorf("summary point %d cannot be represented on the millisecond timeline", index)
		}
		itemID := fmt.Sprintf("%s_p%d", editorialStableID("eds", src.PlanID, composition.CompositionID, member.MemberID), index+1)
		memberIDs[index] = itemID
		items[index] = semanticItem{
			ID: itemID, SceneID: composition.SceneID,
			Kind: string(KindImportantPhrase), Template: "IMPORTANT_PHRASE", PresetID: PhraseDefaultPresetID,
			Text: point.Text, StartMS: pointStartMS, EndMS: pointEndMS,
		}
	}
	return groupID, memberIDs, items, startMS, endMS, nil
}

func editorialStableID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil)[:8])
}

func floorMicrosecondsToMilliseconds(value int64) int64 { return value / 1000 }

func ceilMicrosecondsToMilliseconds(value int64) int64 {
	if value <= 0 {
		return value / 1000
	}
	quotient := value / 1000
	if value%1000 != 0 {
		return quotient + 1
	}
	return quotient
}

func int64Pointer(value int64) *int64 { return &value }

func maxInt64() int64 { return int64(^uint64(0) >> 1) }
