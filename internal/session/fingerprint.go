package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

const fingerprintVersion = "session-history-v1"

func prefixHashes(key []byte, clientKeyID string, items []historyItem) []string {
	seed := hmac.New(sha256.New, key)
	writeField(seed, []byte(fingerprintVersion))
	writeField(seed, []byte(clientKeyID))
	previous := seed.Sum(nil)
	result := make([]string, 0, len(items))
	for _, item := range items {
		hash := hmac.New(sha256.New, key)
		writeField(hash, previous)
		writeField(hash, []byte(item.kind))
		writeField(hash, []byte(item.value))
		previous = hash.Sum(nil)
		result = append(result, hex.EncodeToString(previous))
	}
	return result
}

func writeField(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(value)
}
