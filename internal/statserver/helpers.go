package statserver

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
func numberPtr(v any) *float64 {
	if v == nil {
		return nil
	}
	f := number(v)
	return &f
}
func floatPtr(v float64) *float64 { return &v }
func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func nullTime(v sql.NullTime) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func nullFloat(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}
func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func parseLimit(v string) int {
	n, _ := strconv.Atoi(v)
	if n < 1 {
		n = 100
	}
	if n > 500 {
		n = 500
	}
	return n
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func randomToken() string               { b := make([]byte, 32); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func hashString(v string) string        { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func hashPassword(v string) string      { return "sha256$" + hashString(v) }
func checkPassword(hash, v string) bool { return subtleConstant(hash, hashPassword(v)) }
func subtleConstant(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}
func getenvDefault(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
func joinErrors(errs []string) string { return strings.Join(errs, "; ") }
