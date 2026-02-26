package internal

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path"
	"strings"
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

func ChecksumFromPath(value string) string {
	if value == "" {
		return ""
	}
	switch uri, _ := url.Parse(value); uri.Scheme {
	case "oci":
		_, tag, ok := strings.Cut(path.Base(uri.Path), ":")
		if !ok {
			return ""
		}
		if sha, ok := strings.CutPrefix(tag, "sha256_"); ok {
			return sha
		}
		return ""
	default:
		base := path.Base(uri.Path)
		if sha, ok := strings.CutPrefix(base, "sha256_"); ok {
			return sha
		}
		if _, sha, ok := strings.Cut(base, "_sha256_"); ok {
			return sha
		}
		return ""
	}
}
