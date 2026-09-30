//go:build !windows && !android

package tui

// Terminal-native OSC52 fallback avoids an external xclip/pbcopy dependency.
func setSystemClipboard(string) (bool, error) { return false, nil }
