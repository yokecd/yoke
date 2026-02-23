package internal

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
)

func SHA1HexString(data []byte) string {
	return hex.EncodeToString(SHA1(data))
}

func SHA1HexFromString(data string) string {
	return SHA1HexString([]byte(data))
}

func SHA1(data []byte) []byte {
	hash := sha1.New()
	hash.Write(data)
	return hash.Sum(nil)
}

func SHA256HexString(data []byte) string {
	return hex.EncodeToString(SHA256(data))
}

func SHA256(data []byte) []byte {
	hash := sha256.New()
	hash.Write(data)
	return hash.Sum(nil)
}
