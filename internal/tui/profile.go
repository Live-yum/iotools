package tui

import "github.com/Live-yum/iotools/internal/config"

// New selects the initial default. Later, an empty UI profile means the user
// explicitly chose no profile. Prevent engine entry points from applying their
// CLI-style default fallback, without changing the original collection.
func explicitProfileCollection(c *config.Collection, profile string) *config.Collection {
	if profile != "" || c.DefaultProfile == "" {
		return c
	}
	selected := *c
	selected.DefaultProfile = ""
	return &selected
}
