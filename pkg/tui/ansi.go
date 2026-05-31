package tui

// stripANSIForGoTUI removes SGR escape sequences from s. Pre-rendered ANSI
// strings must be stripped before their visible width is measured, since
// embedded escape codes would otherwise corrupt column math.
func stripANSIForGoTUI(s string) string {
	return uiANSIPattern.ReplaceAllString(s, "")
}
