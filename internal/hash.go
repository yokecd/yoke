package internal

import (
	"crypto/sha1"
	"encoding/hex"
)

func SHA1HexString(data []byte) string {
	hash := sha1.New()
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}
