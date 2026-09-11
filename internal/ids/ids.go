package ids

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sync/atomic"
)

var (
	emergencyInstance [8]byte
	emergencyCounter  atomic.Uint64
)

func init() { _, _ = rand.Read(emergencyInstance[:]) }

func New() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		counter := emergencyCounter.Add(1)
		var suffix [8]byte
		binary.BigEndian.PutUint64(suffix[:], counter)
		return hex.EncodeToString(emergencyInstance[:]) + hex.EncodeToString(suffix[:])
	}
	return hex.EncodeToString(value)
}
