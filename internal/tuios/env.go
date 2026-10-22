package tuios

import "strings"

// ScrubbedEnvVars are pane-identity variables a child must never inherit:
// they would point it at the launching terminal's daemon or pane.
var ScrubbedEnvVars = []string{
	"TUIOS_SOCKET",
	"TUIOS_SESSION",
	"TUIOS_PANE_ID",
	"TUIOS_WINDOW_ID",
	"TUIOS_PANE_TOKEN",
	"TUIOS_PANE_GRANTS",
}

// ScrubEnv returns env without ScrubbedEnvVars or TUIOS_SERVER, then appends TUIOS_SERVER=server.
func ScrubEnv(env []string, server string) []string {
	drop := map[string]bool{"TUIOS_SERVER": true}
	for _, k := range ScrubbedEnvVars {
		drop[k] = true
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TUIOS_SERVER="+server)
}
