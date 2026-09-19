package agentloop

import (
	"regexp"
	"strings"
)

// dangerPatterns add confirm emphasis only, never skip or soften confirm.
var dangerPatterns = []struct {
	re   *regexp.Regexp
	note string
}{
	{regexp.MustCompile(`(?i)\brm\s+.*-[a-z]*r`), "recursive remove"},
	{regexp.MustCompile(`(?i)\bdd\b`), "raw disk write (dd)"},
	{regexp.MustCompile(`(?i)\bmkfs\b`), "filesystem format (mkfs)"},
	{regexp.MustCompile(`:\(\)\s*\{`), "fork bomb pattern"},
	{regexp.MustCompile(`>{1,2}\s*/dev/`), "redirect into a device file"},
	{regexp.MustCompile(`(?i)\bchmod\s+-R\s+/(\s|$)`), "recursive chmod from /"},
	{regexp.MustCompile(`(?i)\bchown\s+-R\s+/(\s|$)`), "recursive chown from /"},
	{regexp.MustCompile(`push\s+--?force`), "git push --force"},
	{regexp.MustCompile(`(?i)\breset\s+--hard\b`), "git reset --hard"},
	{regexp.MustCompile(`(?i)\bdrop\s+(table|database)\b`), "DROP TABLE/DATABASE"},
	{regexp.MustCompile(`(?i)\bdelete\s+from\b`), "DELETE FROM"},
	{regexp.MustCompile(`\|\s*(sh|bash|zsh|fish)\b`), "piping to a shell"},
	{regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff)\b`), "shutdown/reboot"},
	{regexp.MustCompile(`(?i)\bkill\s+(-9\s+)?1\b`), "kill pid 1"},
}

// benignDeviceRedirect excludes harmless sinks (no lookahead in Go regexp).
var benignDeviceRedirect = regexp.MustCompile(`>{1,2}\s*/dev/(null|stdout|stderr)(\s|$|;)`)

// FlagDanger reports matches; (false, "") means no extra emphasis, never safe.
func FlagDanger(command string) (bool, string) {
	scrubbed := benignDeviceRedirect.ReplaceAllString(command, " ")
	var notes []string
	for _, p := range dangerPatterns {
		if p.re.MatchString(scrubbed) {
			notes = append(notes, p.note)
		}
	}
	if len(notes) == 0 {
		return false, ""
	}
	return true, strings.Join(notes, "; ")
}
