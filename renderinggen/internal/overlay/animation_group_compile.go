// animation_group_compile.go owns the heterogeneous animation-group contract.
//
// One animation group is the single animation owner of its members: it assigns
// each member's start/end window from the group timeline (stagger included) and
// one motion per role, then the existing per-kind lowering runs unchanged. No
// second renderer and no new track vocabulary are introduced: the group only
// orchestrates certified motions that already exist in the runtime catalog.
//
// Ownership is fail-closed. A member that already carries its own motion (or a
// composite's group_motion_id) is rejected instead of being silently overridden,
// because two owners for one animated property are the exact drift this contract
// exists to prevent. Legacy plans without animation_groups are untouched.
package overlay

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

const (
	// AnimationGroupChoreographySequenceV1 is the only choreography published
	// in V1: member order is reading order, stagger_frames spaces entrances,
	// and every member holds to its end_offset_ms (or the group end).
	AnimationGroupChoreographySequenceV1 = "sequence_v1"

	AnimationGroupRoleImage   = "image"
	AnimationGroupRolePhrase  = string(KindImportantPhrase)
	AnimationGroupRoleCaption = "caption"
	AnimationGroupRoleDate    = "date"
	AnimationGroupRoleMetric  = "metric"

	animationGroupMinimumMembers        = 2
	animationGroupMaximumMembers        = 8
	animationGroupMaximumStaggerFrames  = 240
	animationGroupStableSelectionDomain = "runtime_default_animation_group_motion"
)

var animationGroupRoleSet = map[string]struct{}{
	AnimationGroupRoleImage:   {},
	AnimationGroupRolePhrase:  {},
	AnimationGroupRoleCaption: {},
	AnimationGroupRoleDate:    {},
	AnimationGroupRoleMetric:  {},
}

type animationGroupMemberKey struct {
	itemID string
	role   string
}

type animationGroupAssignment struct {
	item         *semanticItem
	role         string
	motionID     string
	motionParams map[string]any
	startMS      int64
	endMS        int64
}

// resolveAnimationGroups validates every declared group and applies its timing
// and motion ownership in one pass. It runs after the historical animation
// policies (so the two selector generations cannot silently stack) and before
// validateDeprecatedMotions / item lowering.
func resolveAnimationGroups(src *semanticPlan) error {
	if src == nil || len(src.AnimationGroups) == 0 {
		return nil
	}
	if src.FPSNum <= 0 || src.FPSDen <= 0 {
		return fmt.Errorf("overlay: animation_groups require a positive fps")
	}
	items := make(map[string]*semanticItem, len(src.Items))
	for index := range src.Items {
		items[src.Items[index].ID] = &src.Items[index]
	}
	groupIDs := make(map[string]struct{}, len(src.AnimationGroups))
	claimed := make(map[animationGroupMemberKey]string)
	claimedItems := make(map[string]string)
	assignments := make([]animationGroupAssignment, 0)

	for index := range src.AnimationGroups {
		group := &src.AnimationGroups[index]
		if err := resolveAnimationGroup(src, group, items, groupIDs, claimed, claimedItems, &assignments); err != nil {
			return fmt.Errorf("overlay: animation_groups[%d] (%q): %w", index, group.ID, err)
		}
	}
	for _, assignment := range assignments {
		applyAnimationGroupAssignment(assignment)
	}
	return nil
}

func resolveAnimationGroup(
	src *semanticPlan,
	group *semanticAnimationGroup,
	items map[string]*semanticItem,
	groupIDs map[string]struct{},
	claimed map[animationGroupMemberKey]string,
	claimedItems map[string]string,
	assignments *[]animationGroupAssignment,
) error {
	if strings.TrimSpace(group.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if err := validateAnimationGroupIDs(group.ID, ""); err != nil {
		return err
	}
	if _, exists := groupIDs[group.ID]; exists {
		return fmt.Errorf("duplicate animation group id")
	}
	groupIDs[group.ID] = struct{}{}

	choreography := strings.TrimSpace(group.ChoreographyID)
	if choreography == "" {
		choreography = AnimationGroupChoreographySequenceV1
	}
	if choreography != AnimationGroupChoreographySequenceV1 {
		return fmt.Errorf("unsupported choreography_id %q", choreography)
	}
	if group.StartMS < 0 || group.EndMS <= group.StartMS {
		return fmt.Errorf("start_ms/end_ms must describe a positive window")
	}
	staggerFrames, err := animationGroupStaggerFrames(group.Params)
	if err != nil {
		return err
	}
	if len(group.Members) < animationGroupMinimumMembers || len(group.Members) > animationGroupMaximumMembers {
		return fmt.Errorf("requires %d..%d members, got %d", animationGroupMinimumMembers, animationGroupMaximumMembers, len(group.Members))
	}

	durationMS := group.EndMS - group.StartMS
	staggerMS := int64(math.Round(float64(staggerFrames) * 1000 * float64(src.FPSDen) / float64(src.FPSNum)))
	poolCache := make(map[string][]string)
	stableSelections := make(map[string]string)

	for memberIndex := range group.Members {
		member := &group.Members[memberIndex]
		role := strings.TrimSpace(member.Role)
		if _, ok := animationGroupRoleSet[role]; !ok {
			return fmt.Errorf("member %q has unsupported role %q", member.ItemID, member.Role)
		}
		if strings.TrimSpace(member.ItemID) == "" || strings.TrimSpace(member.ItemID) != member.ItemID {
			return fmt.Errorf("member %d has an empty or untrimmed item_id", memberIndex)
		}
		item, ok := items[member.ItemID]
		if !ok {
			return fmt.Errorf("member %q references an item that does not exist", member.ItemID)
		}
		spec := templateSpecFor(item.Template)
		kind, err := spec.resolveKind(item.Kind, item.ID)
		if err != nil {
			return fmt.Errorf("member %q: %w", member.ItemID, err)
		}
		if err := animationGroupRoleCompatibility(*item, kind, role); err != nil {
			return fmt.Errorf("member %q role %q: %w", member.ItemID, role, err)
		}
		// The item's start/end fields are shared by every layer it lowers, so an
		// item cannot be split across groups even when each group claims a
		// different role: the second group would overwrite the first's window.
		if owner, exists := claimedItems[member.ItemID]; exists && owner != group.ID {
			return fmt.Errorf("member %q is already owned by animation group %q; an item can belong to only one group", member.ItemID, owner)
		}
		claimedItems[member.ItemID] = group.ID

		key := animationGroupMemberKey{itemID: member.ItemID, role: role}
		if owner, exists := claimed[key]; exists {
			return fmt.Errorf("member %q role %q is already claimed by animation group %q", member.ItemID, role, owner)
		}
		claimed[key] = group.ID

		// Any legacy owner on the item conflicts with the group, not only the
		// field matching this member's role: the group moves the item window, so
		// a motion left on the other role would animate against a timeline the
		// group did not author.
		if strings.TrimSpace(item.MotionID) != "" {
			return fmt.Errorf("member %q role %q already owns motion_id %q; an animation group must be the only animation owner", member.ItemID, role, item.MotionID)
		}
		if strings.TrimSpace(item.CaptionMotionID) != "" {
			return fmt.Errorf("member %q role %q already owns caption_motion_id %q; an animation group must be the only animation owner", member.ItemID, role, item.CaptionMotionID)
		}
		if strings.TrimSpace(item.GroupMotionID) != "" {
			return fmt.Errorf("member %q role %q already owns group_motion_id %q; an animation group must be the only animation owner", member.ItemID, role, item.GroupMotionID)
		}

		motionID, err := animationGroupMemberMotion(src, group.ID, role, member, poolCache, stableSelections)
		if err != nil {
			return fmt.Errorf("member %q role %q: %w", member.ItemID, role, err)
		}
		definition, err := resolveMotionDefinition(motionID)
		if err != nil || definition == nil {
			return fmt.Errorf("member %q role %q: motion %q did not resolve", member.ItemID, role, motionID)
		}
		if err := motion.ValidateDefinition(*definition); err != nil {
			return fmt.Errorf("member %q role %q: %w", member.ItemID, role, err)
		}
		if err := animationGroupMotionMetadata(definition); err != nil {
			return fmt.Errorf("member %q role %q: %w", member.ItemID, role, err)
		}
		if !animationGroupRestPose(definition) {
			return fmt.Errorf("member %q role %q: motion %q does not rest at its neutral pose", member.ItemID, role, motionID)
		}
		if len(member.MotionParams) > 0 {
			if _, _, err := motionWindows(member.MotionParams, 0); err != nil {
				return fmt.Errorf("member %q role %q: %w", member.ItemID, role, err)
			}
		}

		offsetMS := int64(memberIndex) * staggerMS
		if member.OffsetMS != nil {
			offsetMS = *member.OffsetMS
		}
		if offsetMS < 0 || offsetMS >= durationMS {
			return fmt.Errorf("member %q role %q offset_ms %d is outside the group window", member.ItemID, role, offsetMS)
		}
		endOffsetMS := durationMS
		if member.EndOffsetMS != nil {
			endOffsetMS = *member.EndOffsetMS
		}
		if endOffsetMS <= offsetMS || endOffsetMS > durationMS {
			return fmt.Errorf("member %q role %q end_offset_ms %d is outside the group window", member.ItemID, role, endOffsetMS)
		}

		*assignments = append(*assignments, animationGroupAssignment{
			item:         item,
			role:         role,
			motionID:     motionID,
			motionParams: cloneMotionParams(member.MotionParams),
			startMS:      group.StartMS + offsetMS,
			endMS:        group.StartMS + endOffsetMS,
		})
	}
	return nil
}

func animationGroupMemberMotion(
	src *semanticPlan,
	groupID, role string,
	member *semanticAnimationGroupMember,
	pools map[string][]string,
	stable map[string]string,
) (string, error) {
	explicit := member.MotionID
	if explicit != strings.TrimSpace(explicit) {
		return "", fmt.Errorf("motion_id must be trimmed")
	}
	if explicit != "" {
		if err := motionDeprecationError(explicit); err != nil {
			return "", err
		}
		target, _ := animationGroupRoleTarget(role)
		if !motionAdmitsTarget(explicit, target) {
			return "", fmt.Errorf("motion %q is not supported for target %q", explicit, target)
		}
		return explicit, nil
	}
	if len(member.MotionParams) > 0 {
		return "", fmt.Errorf("motion_params requires motion_id")
	}
	pool, ok := pools[role]
	if !ok {
		pool = animationGroupRolePool(role)
		pools[role] = pool
	}
	if len(pool) == 0 {
		return "", fmt.Errorf("no selectable motion exists for role %q", role)
	}
	if selected, ok := stable[role]; ok {
		return selected, nil
	}
	selected := animationGroupStableMotion(src, groupID, role, pool)
	stable[role] = selected
	return selected, nil
}

func animationGroupStaggerFrames(params map[string]any) (int64, error) {
	if len(params) == 0 {
		return 0, nil
	}
	var raw any
	for key, value := range params {
		if key != "stagger_frames" {
			return 0, fmt.Errorf("unsupported params.%s", key)
		}
		raw = value
	}
	if raw == nil {
		return 0, nil
	}
	value, ok := numericValue(raw)
	if !ok || math.Trunc(value) != value || value < 0 || value > animationGroupMaximumStaggerFrames {
		return 0, fmt.Errorf("params.stagger_frames must be an integer in 0..%d", animationGroupMaximumStaggerFrames)
	}
	return int64(value), nil
}

func animationGroupRoleTarget(role string) (string, bool) {
	switch role {
	case AnimationGroupRoleImage:
		return TargetImage, true
	case AnimationGroupRolePhrase:
		return TargetImportantPhrase, true
	case AnimationGroupRoleCaption:
		return TargetCaption, true
	case AnimationGroupRoleDate:
		return TargetDate, true
	case AnimationGroupRoleMetric:
		return TargetMetric, true
	default:
		return "", false
	}
}

func animationGroupRoleCompatibility(item semanticItem, kind ItemKind, role string) error {
	switch role {
	case AnimationGroupRoleImage:
		if len(item.ImageLayers) > 0 {
			return fmt.Errorf("composite image_layers keep their own group_motion_id and are not members of an animation group")
		}
		if !isImageKind(kind) && !isEntityKind(kind) {
			return fmt.Errorf("item kind %q does not lower to an image", kind)
		}
		if len(item.Assets) == 0 {
			return fmt.Errorf("item has no image asset")
		}
	case AnimationGroupRoleCaption:
		if strings.TrimSpace(item.EntityCaption) == "" {
			return fmt.Errorf("item has no entity_caption to animate")
		}
		if len(item.ImageLayers) > 0 {
			return fmt.Errorf("caption role does not support composite image_layers")
		}
		if !isImageKind(kind) && !isEntityKind(kind) {
			return fmt.Errorf("item kind %q does not own a caption layer", kind)
		}
		if len(item.Assets) == 0 {
			return fmt.Errorf("caption item has no image asset")
		}
	case AnimationGroupRolePhrase:
		if kind != KindImportantPhrase {
			return fmt.Errorf("item kind %q is not an important phrase", kind)
		}
		if len(item.Assets) > 0 || len(item.ImageLayers) > 0 {
			return fmt.Errorf("important phrase role requires a text-only item")
		}
	case AnimationGroupRoleDate, AnimationGroupRoleMetric:
		target, _ := animationGroupRoleTarget(role)
		if got := semanticTextMotionTarget(item, kind); got != target {
			return fmt.Errorf("item resolves to motion target %q, not %q", got, target)
		}
		if len(item.ImageLayers) > 0 {
			return fmt.Errorf("%s role does not support composite image_layers", role)
		}
	}
	return nil
}

// animationGroupRolePool returns the certified, selectable motions a role may
// choose from. The pools are the same ones the runtime already uses for the
// corresponding target, filtered by target admission, definition validity,
// 3D/camera truthfulness and the neutral resting pose the group requires.
func animationGroupRolePool(role string) []string {
	var ids []string
	switch role {
	case AnimationGroupRoleImage:
		ids = append(ids, runtimeDefaultImageMotionIDs...)
	case AnimationGroupRolePhrase:
		ids = append(ids, motion.Registry.PhraseAnimationIDs()...)
	case AnimationGroupRoleCaption:
		ids = append(ids, runtimeDefaultCaptionMotionIDs...)
	case AnimationGroupRoleDate:
		ids = append(ids, motion.Registry.PresentationMotionIDs("date_v1")...)
	case AnimationGroupRoleMetric:
		ids = append(ids, motion.Registry.PresentationMotionIDs("metric_v1")...)
	}
	target, ok := animationGroupRoleTarget(role)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	pool := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		if !motion.Registry.Selectable(id) || !motionAdmitsTarget(id, target) {
			continue
		}
		definition, err := resolveMotionDefinition(id)
		if err != nil || definition == nil {
			continue
		}
		if err := motion.ValidateDefinition(*definition); err != nil {
			continue
		}
		if err := animationGroupMotionMetadata(definition); err != nil {
			continue
		}
		if !animationGroupRestPose(definition) {
			continue
		}
		seen[id] = struct{}{}
		pool = append(pool, id)
	}
	sort.Strings(pool)
	return pool
}

func animationGroupStableMotion(src *semanticPlan, groupID, role string, pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	index := styleHash(animationGroupStableSelectionDomain, src.PlanID, src.VideoID, groupID+"\x00"+role, uint64(len(pool)))
	return pool[index]
}

func animationGroupMotionMetadata(definition *motion.MotionDefinition) error {
	if definition == nil {
		return fmt.Errorf("motion definition is nil")
	}
	has3D := animationGroupDefinitionHas3D(definition)
	if definition.Requires3D != nil && *definition.Requires3D != has3D {
		return fmt.Errorf("motion %q requires_3d=%v but its tracks are 3D=%v", definition.ID, *definition.Requires3D, has3D)
	}
	if definition.RequiresCamera != nil && *definition.RequiresCamera != has3D {
		return fmt.Errorf("motion %q requires_camera=%v but its tracks are camera-backed=%v", definition.ID, *definition.RequiresCamera, has3D)
	}
	return nil
}

// animationGroupDefinitionHas3D is the single derivation shared by group
// validation and the catalog preview: only actual camera-backed tracks count,
// never the authored Requires3D/RequiresCamera flags the tracks are checked
// against.
func animationGroupDefinitionHas3D(definition *motion.MotionDefinition) bool {
	if definition == nil {
		return false
	}
	has3D := false
	for _, track := range definition.Tracks {
		has3D = has3D || motion.IsCameraBacked3DProperty(track.Property)
	}
	for _, animator := range definition.TextAnimators {
		for _, track := range animator.Properties {
			has3D = has3D || motion.IsCameraBacked3DProperty(track.Property)
		}
	}
	return has3D
}

func animationGroupRestPose(definition *motion.MotionDefinition) bool {
	if definition == nil {
		return false
	}
	for _, track := range definition.Tracks {
		if !animationGroupTrackRests(track.Property, track.Keyframes) {
			return false
		}
	}
	for _, animator := range definition.TextAnimators {
		for _, track := range animator.Properties {
			if !animationGroupTrackRests(track.Property, track.Keyframes) {
				return false
			}
		}
	}
	return true
}

func animationGroupTrackRests(property string, keyframes []motion.AnimationKeyframe) bool {
	if len(keyframes) < 2 {
		return false
	}
	value, ok := motion.NumericValue(keyframes[len(keyframes)-1].Value)
	if !ok {
		// Color/non-scalar tracks are not part of the neutral pose contract.
		return true
	}
	switch property {
	case "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "rotation_z":
		return value == 0
	case "scale", "scale_x", "scale_y", "opacity":
		return value == 1
	default:
		return true
	}
}

func applyAnimationGroupAssignment(assignment animationGroupAssignment) {
	assignment.item.StartMS = assignment.startMS
	assignment.item.EndMS = assignment.endMS
	// The group owns the window. Dropping the canonical microsecond projection
	// keeps the legacy millisecond fields authoritative for the item pass.
	assignment.item.StartUS = nil
	assignment.item.DurationUS = nil
	if assignment.item.DurationMS != nil {
		duration := assignment.endMS - assignment.startMS
		assignment.item.DurationMS = &duration
	}
	if assignment.role == AnimationGroupRoleCaption {
		assignment.item.CaptionMotionID = assignment.motionID
		assignment.item.CaptionMotionParams = assignment.motionParams
		return
	}
	assignment.item.MotionID = assignment.motionID
	assignment.item.MotionParams = assignment.motionParams
}
