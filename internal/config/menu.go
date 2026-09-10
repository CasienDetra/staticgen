package config

import "sort"

// sortMenu orders menu entries by weight, then name, so navigation is stable
// regardless of the order keys appear in the config file.
func sortMenu(items []MenuItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Weight != items[j].Weight {
			return items[i].Weight < items[j].Weight
		}
		return items[i].Name < items[j].Name
	})
}
