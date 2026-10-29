package cli

import "encoding/json"

// managedAddresses collects managed resource instance addresses from a
// `terraform show -json` plan. resource_changes includes creates, updates,
// deletes, and no-ops. planned_values and prior_state are unioned in so a
// no-op plan or a destroy of an orphan still yields a complete set.
func managedAddresses(plan []byte) ([]string, error) {
	var root map[string]any
	if err := json.Unmarshal(plan, &root); err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	if rcs, ok := root["resource_changes"].([]any); ok {
		for _, rc := range rcs {
			addManaged(set, rc)
		}
	}
	if pv, ok := root["planned_values"].(map[string]any); ok {
		if mod, ok := pv["root_module"].(map[string]any); ok {
			walkModule(set, mod)
		}
	}
	if ps, ok := root["prior_state"].(map[string]any); ok {
		if vals, ok := ps["values"].(map[string]any); ok {
			if mod, ok := vals["root_module"].(map[string]any); ok {
				walkModule(set, mod)
			}
		}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	return out, nil
}

func walkModule(set map[string]struct{}, mod map[string]any) {
	if rs, ok := mod["resources"].([]any); ok {
		for _, r := range rs {
			addManaged(set, r)
		}
	}
	if kids, ok := mod["child_modules"].([]any); ok {
		for _, k := range kids {
			if m, ok := k.(map[string]any); ok {
				walkModule(set, m)
			}
		}
	}
}

func addManaged(set map[string]struct{}, raw any) {
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	if mode, ok := m["mode"].(string); ok && mode != "managed" {
		return
	}
	addr, ok := m["address"].(string)
	if !ok || addr == "" {
		return
	}
	set[addr] = struct{}{}
}
