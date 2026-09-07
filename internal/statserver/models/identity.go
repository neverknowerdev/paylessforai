package models

import (
	"regexp"
	"strings"
)

var separator = regexp.MustCompile(`[^a-z0-9]+`)

// Normalize preserves meaningful revision/date tokens while making formatting aliases comparable.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = separator.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func NormalizeBenchmark(value string) string { return Normalize(value) }

func CanonicalSlug(creator, name, revision string) string {
	parts := []string{Normalize(creator), Normalize(name), Normalize(revision)}
	return strings.Trim(strings.ReplaceAll(strings.Join(parts, "-"), " ", "-"), "-")
}
