package statserver

import (
	"strings"
	"unicode"
)

func Normalize(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	space := false
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}
func NormalizeBenchmark(v string) string {
	return Normalize(strings.NewReplacer("_", " ", "-", " ").Replace(v))
}
func canonicalSlug(creator, name, revision string) string {
	v := Normalize(strings.Trim(strings.Join([]string{creator, name, revision}, " "), " "))
	return strings.ReplaceAll(v, " ", "-")
}
