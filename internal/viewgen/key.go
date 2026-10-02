package viewgen

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// multiplexers are the programs whose second word names a real subcommand.
// Everything else keys on the program alone, so "ps -U me" keys as "ps".
var multiplexers = map[string]bool{
	"git": true, "go": true, "docker": true, "kubectl": true, "make": true,
	"npm": true, "yarn": true, "pnpm": true, "cargo": true, "brew": true,
	"apt": true, "pip": true, "systemctl": true,
}

// Normalise reduces a command to its shape, so "git status
// --porcelain=v1 --branch" and "git status -sb" share one view.
func Normalise(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	if !multiplexers[name] {
		return name
	}
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		if strings.ContainsAny(f, "/.") {
			break
		}
		return name + " " + f
	}
	return name
}

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

// Key identifies a saved view. The judged kind is part of it because
// "ls" is a file_listing and "ls -la" a table, which cannot share a parse.
func Key(command, kind string) string {
	shape := Normalise(command)
	sum := sha256.Sum256([]byte(shape + "\x00" + kind))
	name := unsafeName.ReplaceAllString(strings.ToLower(shape+"-"+kind), "-")
	return strings.Trim(name, "-") + "-" + hex.EncodeToString(sum[:4])
}
