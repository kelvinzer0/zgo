package canonical

import (
	"fmt"
	"strings"
)

// Allowed GOFLAGS prefixes/exact flags
// Diizinkan: -tags, -trimpath, -buildvcs, -ldflags (hanya -s -w -X), -gcflags (terbatas)
// Ditolak: -toolexec, -exec, -modfile, -overlay, -mod=vendor, -pkgdir, -extldflags, path lokal apa pun
var forbiddenFlags = []string{
	"-toolexec",
	"-exec",
	"-modfile",
	"-overlay",
	"-mod=vendor",
	"-pkgdir",
	"-extldflags",
}

// splitFlags splits a string into tokens while preserving quoted substrings
func splitFlags(s string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for _, r := range s {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteRune(r)
		case (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// ValidateAndNormalizeGOFLAGS validates goflags against strict allowlist and normalizes them.
// Returns an error if any disallowed or unrecognized flag is found.
func ValidateAndNormalizeGOFLAGS(rawFlags string) ([]string, error) {
	trimmed := strings.TrimSpace(rawFlags)
	if trimmed == "" {
		return []string{}, nil
	}

	parts := splitFlags(trimmed)
	var normalized []string

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if part == "" {
			continue
		}

		// Check explicitly forbidden flags first
		for _, forbidden := range forbiddenFlags {
			if part == forbidden || strings.HasPrefix(part, forbidden+"=") {
				return nil, fmt.Errorf("ERROR: unsupported GOFLAGS: %s", part)
			}
		}

		// Check for local file path patterns (e.g. /path, ./path, ../path, C:\, etc.)
		if strings.HasPrefix(part, "/") || strings.HasPrefix(part, "./") || strings.HasPrefix(part, "../") || strings.Contains(part, `:\`) {
			return nil, fmt.Errorf("ERROR: local filesystem paths are forbidden in GOFLAGS: %s", part)
		}

		// Allowlist verification
		switch {
		case part == "-trimpath":
			normalized = append(normalized, "-trimpath")

		case part == "-buildvcs" || part == "-buildvcs=true" || part == "-buildvcs=false" || part == "-buildvcs=auto":
			normalized = append(normalized, part)

		case strings.HasPrefix(part, "-tags=") || strings.HasPrefix(part, "-tags"):
			val := ""
			if strings.HasPrefix(part, "-tags=") {
				val = strings.TrimPrefix(part, "-tags=")
			} else if part == "-tags" && i+1 < len(parts) {
				i++
				val = parts[i]
			} else {
				return nil, fmt.Errorf("ERROR: malformed -tags flag")
			}
			// Validate tags contain only alphanumeric, comma, underscore
			for _, r := range val {
				if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != ',' && r != '.' {
					return nil, fmt.Errorf("ERROR: invalid tag character in -tags: %s", val)
				}
			}
			normalized = append(normalized, "-tags="+val)

		case strings.HasPrefix(part, "-ldflags=") || strings.HasPrefix(part, "-ldflags"):
			val := ""
			if strings.HasPrefix(part, "-ldflags=") {
				val = strings.TrimPrefix(part, "-ldflags=")
			} else if part == "-ldflags" && i+1 < len(parts) {
				i++
				val = parts[i]
			} else {
				return nil, fmt.Errorf("ERROR: malformed -ldflags flag")
			}
			// Validate ldflags: only -s, -w, -X key=val allowed
			cleaned, err := validateLdflags(val)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, "-ldflags="+cleaned)

		case strings.HasPrefix(part, "-gcflags=") || strings.HasPrefix(part, "-gcflags"):
			val := ""
			if strings.HasPrefix(part, "-gcflags=") {
				val = strings.TrimPrefix(part, "-gcflags=")
			} else if part == "-gcflags" && i+1 < len(parts) {
				i++
				val = parts[i]
			} else {
				return nil, fmt.Errorf("ERROR: malformed -gcflags flag")
			}
			// Limited gcflags allowed: -N, -l, -m
			cleaned, err := validateGcflags(val)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, "-gcflags="+cleaned)

		default:
			return nil, fmt.Errorf("ERROR: unsupported GOFLAGS: %s", part)
		}
	}

	return normalized, nil
}

// validateLdflags ensures only -s, -w, and -X importpath.name=value are present
func validateLdflags(raw string) (string, error) {
	// Strip enclosing quotes if present
	s := strings.Trim(strings.TrimSpace(raw), `"'`)
	tokens := strings.Fields(s)
	var allowed []string

	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t == "-s" || t == "-w" {
			allowed = append(allowed, t)
		} else if t == "-X" {
			if i+1 >= len(tokens) {
				return "", fmt.Errorf("ERROR: -ldflags -X requires an argument")
			}
			i++
			arg := tokens[i]
			if !strings.Contains(arg, "=") {
				return "", fmt.Errorf("ERROR: -ldflags -X argument must be pkg.var=val: %s", arg)
			}
			// ensure no subshell or command injection characters
			if strings.ContainsAny(arg, "`$();|&<>") {
				return "", fmt.Errorf("ERROR: illegal characters in -X value: %s", arg)
			}
			allowed = append(allowed, "-X", arg)
		} else if strings.HasPrefix(t, "-X=") {
			arg := strings.TrimPrefix(t, "-X=")
			if !strings.Contains(arg, "=") {
				return "", fmt.Errorf("ERROR: -ldflags -X argument must be pkg.var=val: %s", arg)
			}
			if strings.ContainsAny(arg, "`$();|&<>") {
				return "", fmt.Errorf("ERROR: illegal characters in -X value: %s", arg)
			}
			allowed = append(allowed, "-X="+arg)
		} else {
			return "", fmt.Errorf("ERROR: unsupported -ldflags option (only -s, -w, -X allowed): %s", t)
		}
	}
	return strings.Join(allowed, " "), nil
}

// validateGcflags ensures only safe gcflags are allowed (-N, -l, -m)
func validateGcflags(raw string) (string, error) {
	s := strings.Trim(strings.TrimSpace(raw), `"'`)
	tokens := strings.Fields(s)
	var allowed []string

	for _, t := range tokens {
		switch t {
		case "-N", "-l", "-m", "-m=1", "-m=2":
			allowed = append(allowed, t)
		default:
			return "", fmt.Errorf("ERROR: unsupported -gcflags option: %s", t)
		}
	}
	return strings.Join(allowed, " "), nil
}
