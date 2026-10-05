package ui

// SortByName resolves a configured sort name to its mode; an unknown name
// falls back to def rather than stopping the view opening.
func SortByName[M comparable](names map[M]string, def M, name string) (M, bool) {
	for mode, n := range names {
		if n == name {
			return mode, true
		}
	}
	return def, false
}

// SortNames lists a view's sorts in display order, for the settings overlay.
func SortNames[M comparable](order []M, names map[M]string) []string {
	out := make([]string, 0, len(order))
	for _, m := range order {
		out = append(out, names[m])
	}
	return out
}
