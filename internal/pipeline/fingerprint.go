package pipeline

import "github.com/hhaidrr/personal-telemetry/internal/ownership"

// Line numbers are left out so the fingerprint survives edits elsewhere in the file.
func fingerprint(excType string, m ownership.Match) string {
	if excType == "" {
		excType = "error"
	}
	return excType + "@" + m.Frame.Path + ":" + m.Frame.Func
}
