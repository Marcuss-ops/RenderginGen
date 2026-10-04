package overlay

import "fmt"

func parsePathCommand(command map[string]any) (LayerPathCommand, error) {
	commandType, ok := command["type"].(string)
	if !ok || !isSupportedPathCommand(commandType) {
		return LayerPathCommand{}, fmt.Errorf("unsupported path command type %q", commandType)
	}
	allowed := map[string]bool{"type": true}
	result := LayerPathCommand{Type: commandType}
	readPair := func(key string) ([]float64, error) {
		allowed[key] = true
		raw, exists := command[key]
		if !exists {
			return nil, fmt.Errorf("missing %s", key)
		}
		return validatePair(raw, "", "path."+key)
	}
	switch commandType {
	case "move_to", "line_to":
		point, err := readPair("point")
		if err != nil {
			return LayerPathCommand{}, err
		}
		result.Point = point
	case "M", "L":
		var pRaw any
		var key string
		if raw, ok := command["point"]; ok {
			pRaw, key = raw, "point"
		} else if raw, ok := command["points"]; ok {
			pRaw, key = raw, "points"
		} else {
			return LayerPathCommand{}, fmt.Errorf("missing point or points")
		}
		allowed[key] = true
		pt, err := validatePair(pRaw, "", "path."+key)
		if err != nil {
			return LayerPathCommand{}, err
		}
		result.Point = pt
		result.Points = pt
	case "quadratic_to", "Q":
		point, pointErr := readPair("point")
		control, controlErr := readPair("control1")
		if pointErr != nil || controlErr != nil {
			return LayerPathCommand{}, fmt.Errorf("quadratic_to requires a finite point and control1")
		}
		result.Point, result.Control1 = point, control
	case "cubic_to", "C":
		point, pointErr := readPair("point")
		control1, control1Err := readPair("control1")
		control2, control2Err := readPair("control2")
		if pointErr != nil || control1Err != nil || control2Err != nil {
			return LayerPathCommand{}, fmt.Errorf("cubic_to requires a finite point, control1 and control2")
		}
		result.Point, result.Control1, result.Control2 = point, control1, control2
	case "close", "Z":
	}
	for key := range command {
		if !allowed[key] {
			return LayerPathCommand{}, fmt.Errorf("path command has unsupported property %q", key)
		}
	}
	return result, nil
}

func parsePath(raw any, itemID string) ([]LayerPathCommand, error) {
	commands, ok := raw.([]any)
	if !ok || len(commands) == 0 || len(commands) > 256 {
		return nil, fmt.Errorf("overlay: item %q shape.path must contain 1..256 commands", itemID)
	}
	result := make([]LayerPathCommand, 0, len(commands))
	for index, entry := range commands {
		command, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q shape.path[%d] must be an object", itemID, index)
		}
		parsed, err := parsePathCommand(command)
		if err != nil {
			return nil, fmt.Errorf("overlay: item %q shape.path[%d]: %w", itemID, index, err)
		}
		if index == 0 && parsed.Type != "move_to" && parsed.Type != "M" {
			return nil, fmt.Errorf("overlay: item %q shape.path must begin with move_to", itemID)
		}
		result = append(result, parsed)
	}
	return result, nil
}
