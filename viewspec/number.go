package viewspec

import (
	"math"
	"strconv"
	"strings"
)

// number reads a quantity the way a shell prints one: 45%, 1,024, 1.2G
// or 1e5. A comma separates thousands, so 1,5 is 15. Unreadable or
// infinite is 0, never a failed view.
func number(s string) float64 {
	digits, unit := splitNumber(strings.TrimSpace(s))
	if digits == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(digits, ",", ""), 64)
	if err != nil {
		return 0
	}
	f *= scale(unit)
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return f
}

// splitNumber cuts s into its leading numeric prefix and whatever
// followed. Separators stay in, and number strips them.
func splitNumber(s string) (digits, unit string) {
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	for ; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && c != '.' && c != ',' {
			break
		}
	}
	// An exponent only when digits follow it, so 1E stays an exabyte.
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '-' || s[j] == '+') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// multipliers are 1024-based, since that is what -h means in the
// commands that print them.
const multipliers = "KMGTPE"

// scale reads a unit suffix, counting only a single uppercase multiplier:
// "12ms" is 12, since a lowercase m is milli as often as not.
func scale(unit string) float64 {
	unit = strings.TrimSuffix(strings.TrimSuffix(unit, "b"), "B")
	unit = strings.TrimSuffix(unit, "i")
	if unit == "k" {
		unit = "K"
	}
	if len(unit) != 1 {
		return 1
	}
	i := strings.IndexByte(multipliers, unit[0])
	if i < 0 {
		return 1
	}
	return math.Pow(1024, float64(i+1))
}
