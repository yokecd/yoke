package internal

import (
	"crypto/sha1"
	"encoding/hex"
)

func SHA1HexString(data []byte) string {
	return hex.EncodeToString(SHA1(data))
}

func SHA1(data []byte) []byte {
	hash := sha1.New()
	hash.Write(data)
	return hash.Sum(nil)
}
