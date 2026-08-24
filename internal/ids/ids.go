package ids

import (
	"crypto/rand"
	"encoding/hex"
)

func New() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "local-id"
	}
	return hex.EncodeToString(value)
}
