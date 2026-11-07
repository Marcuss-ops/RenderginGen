package overlay

// RuntimeAnimationGroupChoreography is the catalog projection of one
// heterogeneous group choreography. It is the preview surface a picker reads:
// which roles a group may contain, how many members it accepts, how the group
// selects motions, and the certified pool behind each role.
type RuntimeAnimationGroupChoreography struct {
	ID             string                      `json:"id"`
	Description    string                      `json:"description"`
	Owner          string                      `json:"owner"`
	Selection      string                      `json:"selection"`
	MinimumMembers int                         `json:"minimum_members"`
	MaximumMembers int                         `json:"maximum_members"`
	Roles          []RuntimeAnimationGroupRole `json:"roles"`
}

// RuntimeAnimationGroupRole is one role's selectable motion pool. The 3D and
// camera flags are derived from the motions' actual tracks, not from a second
// authored metadata field, so the catalog cannot advertise a capability the
// lowered tracks do not require.
type RuntimeAnimationGroupRole struct {
	Role           string   `json:"role"`
	MotionTarget   string   `json:"motion_target"`
	MotionCount    int      `json:"motion_count"`
	MotionIDs      []string `json:"motion_ids"`
	Requires3D     bool     `json:"requires_3d"`
	RequiresCamera bool     `json:"requires_camera"`
}

func runtimeAnimationGroupChoreographies() []RuntimeAnimationGroupChoreography {
	roles := []string{
		AnimationGroupRoleImage,
		AnimationGroupRolePhrase,
		AnimationGroupRoleCaption,
		AnimationGroupRoleDate,
		AnimationGroupRoleMetric,
	}
	choreography := RuntimeAnimationGroupChoreography{
		ID:             AnimationGroupChoreographySequenceV1,
		Description:    "One group owns a shared timeline: members enter in array order, stagger_frames spaces their entrances, and each role keeps one stable motion selection for the whole group.",
		Owner:          "animation_group",
		Selection:      "stable_random_per_role",
		MinimumMembers: animationGroupMinimumMembers,
		MaximumMembers: animationGroupMaximumMembers,
		Roles:          make([]RuntimeAnimationGroupRole, 0, len(roles)),
	}
	for _, role := range roles {
		pool := animationGroupRolePool(role)
		target, _ := animationGroupRoleTarget(role)
		requires3D, requiresCamera := animationGroupPoolCapabilities(pool)
		choreography.Roles = append(choreography.Roles, RuntimeAnimationGroupRole{
			Role:           role,
			MotionTarget:   target,
			MotionCount:    len(pool),
			MotionIDs:      append([]string(nil), pool...),
			Requires3D:     requires3D,
			RequiresCamera: requiresCamera,
		})
	}
	return []RuntimeAnimationGroupChoreography{choreography}
}

func animationGroupPoolCapabilities(pool []string) (bool, bool) {
	has3D := false
	for _, id := range pool {
		definition, err := resolveMotionDefinition(id)
		if err != nil || definition == nil {
			continue
		}
		has3D = has3D || animationGroupDefinitionHas3D(definition)
	}
	// A camera-backed property is the only runtime reason this compiler needs a
	// camera, so the two capabilities share the same derived truth.
	return has3D, has3D
}
