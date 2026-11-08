package membership

import "sort"

// Preset names for project grants.
const (
	PresetViewer       = "viewer"
	PresetDeveloper    = "developer"
	PresetOperator     = "operator"
	PresetProjectAdmin = "project_admin"
	PresetCustom       = "custom"
)

// PresetNames lists the selectable presets in display order.
var PresetNames = []string{PresetViewer, PresetDeveloper, PresetOperator, PresetProjectAdmin, PresetCustom}

func setLevel(m map[Area]Level, l Level, areas ...Area) {
	for _, a := range areas {
		m[a] = l
	}
}

// PresetAreas returns the area map of a named preset (nil, false for custom/unknown).
func PresetAreas(name string) (map[Area]Level, bool) {
	m := map[Area]Level{}
	switch name {
	case PresetViewer:
		setLevel(m, LevelView, AreaOverview, AreaDeployments, AreaBuilds, AreaRuntime, AreaLogs, AreaMetrics, AreaDomains)
	case PresetDeveloper, PresetOperator:
		setLevel(m, LevelView, AreaOverview, AreaDeployments, AreaBuilds, AreaRuntime, AreaLogs, AreaMetrics, AreaDomains,
			AreaRepository, AreaSecrets, AreaAddons, AreaDatabases)
		setLevel(m, LevelManage, AreaDeployments, AreaBuilds, AreaRuntime, AreaStorage)
		if name == PresetOperator {
			setLevel(m, LevelManage, AreaDomains, AreaAddons, AreaDatabases, AreaSecrets, AreaRepository)
		}
	case PresetProjectAdmin:
		for _, a := range AllAreas {
			m[a] = LevelManage
		}
	default:
		return nil, false
	}
	return m, true
}

// PresetSecretsReveal is the default secrets_reveal flag of a preset.
func PresetSecretsReveal(name string) bool { return name == PresetProjectAdmin }

// ValidArea / ValidLevel validate user input.
func ValidArea(a Area) bool {
	for _, x := range AllAreas {
		if x == a {
			return true
		}
	}
	return false
}

func ValidLevel(l Level) bool { return l == LevelNone || l == LevelView || l == LevelManage }

// NormalizeAreas drops "none" entries and returns a deterministic copy.
func NormalizeAreas(in map[Area]Level) map[Area]Level {
	out := map[Area]Level{}
	for a, l := range in {
		if l != LevelNone {
			out[a] = l
		}
	}
	return out
}

// Within reports whether a grant (areas + reveal flag) stays within what the granter holds.
// Org owner/admin (Full) hold everything.
func (a *Access) Within(areas map[Area]Level, reveal bool) bool {
	if a == nil {
		return false
	}
	if a.Full {
		return true
	}
	for area, l := range areas {
		if !a.Level(area).AtLeast(l) {
			return false
		}
	}
	return !reveal || a.CanRevealSecrets()
}

// SortedAreas returns the areas of a map in AllAreas order (stable output for UIs/tests).
func SortedAreas(m map[Area]Level) []Area {
	out := make([]Area, 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	idx := map[Area]int{}
	for i, a := range AllAreas {
		idx[a] = i
	}
	sort.Slice(out, func(i, j int) bool { return idx[out[i]] < idx[out[j]] })
	return out
}

// ProjectScope lists which projects of an org a user may see.
type ProjectScope struct {
	Full       bool
	ProjectIDs map[string]bool
}

// Allows reports whether the project is visible to the user.
func (p ProjectScope) Allows(projectID string) bool { return p.Full || p.ProjectIDs[projectID] }
