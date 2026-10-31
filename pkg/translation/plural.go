package translation

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	exactMatchRegex = regexp.MustCompile(`^\{(\d+)\}\s*(.*)$`)
	rangeMatchRegex = regexp.MustCompile(`^\[([0-9*+-]+)\s*,\s*([0-9*+-]+)\]\s*(.*)$`)
)

// SelectPlural chooses the correct segment from a pluralized string for the given number.
//
// Formats supported:
//  1. Exact: "{0} No apples|{1} One apple|[2,*] Many apples"
//  2. Range: "[1,5] A few|[6,*] Many"
//  3. Simple: "apple|apples" (singular if number == 1, else plural)
func SelectPlural(line string, number int) string {
	segments := strings.Split(line, "|")
	if len(segments) == 0 {
		return line
	}

	// 1. Check for explicit bracket/range segments ({0}, [1,5], [2,*])
	hasExplicitRules := false
	for _, seg := range segments {
		trimmed := strings.TrimSpace(seg)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			hasExplicitRules = true
			break
		}
	}

	if hasExplicitRules {
		for _, seg := range segments {
			trimmed := strings.TrimSpace(seg)

			// Exact match {n}
			if matches := exactMatchRegex.FindStringSubmatch(trimmed); len(matches) == 3 {
				target, err := strconv.Atoi(matches[1])
				if err == nil && target == number {
					return matches[2]
				}
			}

			// Range match [from, to]
			if matches := rangeMatchRegex.FindStringSubmatch(trimmed); len(matches) == 4 {
				fromStr := matches[1]
				toStr := matches[2]
				msg := matches[3]

				from := math.MinInt64
				if fromStr != "*" && fromStr != "-Inf" && fromStr != "-" {
					if v, err := strconv.ParseInt(fromStr, 10, 64); err == nil {
						from = int(v)
					}
				}

				to := math.MaxInt64
				if toStr != "*" && toStr != "+Inf" && toStr != "+" {
					if v, err := strconv.ParseInt(toStr, 10, 64); err == nil {
						to = int(v)
					}
				}

				if int64(number) >= int64(from) && int64(number) <= int64(to) {
					return msg
				}
			}
		}

		// If no rule matched, return the last segment stripped of its selector
		lastSeg := strings.TrimSpace(segments[len(segments)-1])
		if matches := exactMatchRegex.FindStringSubmatch(lastSeg); len(matches) == 3 {
			return matches[2]
		}
		if matches := rangeMatchRegex.FindStringSubmatch(lastSeg); len(matches) == 4 {
			return matches[3]
		}
		return lastSeg
	}

	// 2. Simple singular|plural format
	if len(segments) == 1 {
		return strings.TrimSpace(segments[0])
	}

	if number == 1 {
		return strings.TrimSpace(segments[0])
	}
	return strings.TrimSpace(segments[1])
}
