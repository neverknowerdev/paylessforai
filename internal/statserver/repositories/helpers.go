package repositories

import "encoding/json"

func jsonBytes(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
