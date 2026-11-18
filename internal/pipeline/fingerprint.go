package pipeline

import "github.com/hhaidrr/personal-telemetry/internal/ownership"

// Line numbers are left out so the fingerprint survives edits elsewhere in the file.
// Per environment, so a bug reaching production alerts even if it already
// alerted from develop within the cooldown.
func fingerprint(environment, excType string, m ownership.Match) string {
	if excType == "" {
		excType = "error"
	}
	key := excType + "@" + m.Frame.Path + ":" + m.Frame.Func
	if environment != "" {
		key = environment + "/" + key
	}
	return key
}
