package config

import (
	"crypto/rand"
	"encoding/hex"
)

func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
