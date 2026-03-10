package xcrypto

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/wasm/module"
)

const moduleSignatureKey = "signature"

func ParsePublicKeyFromPEM(data []byte) (key crypto.PublicKey, err error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM")
	}
	switch block.Type {
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "PRIVATE KEY", "RSA PRIVATE KEY":
		key, err := ParsePrivateKeyFromPEM(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		return toPublicKey(key)
	default:
		return nil, fmt.Errorf("unsupported key type: %q", block.Type)
	}
}

func ParsePrivateKeyFromPEM(data []byte) (key crypto.PrivateKey, err error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported key type: %q", block.Type)
	}
}

func VerifyModule(keys PublicKeySet, wasm []byte) error {
	wasm, data := module.WithoutCustomSection(wasm, module.PrefixSchematics+moduleSignatureKey)
	if len(data) == 0 {
		return fmt.Errorf("module is unsigned")
	}

	var payload signaturePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("invalid signature payload: %w", err)
	}

	key, ok := keys[payload.Fingerprint]
	if !ok {
		return fmt.Errorf("module's key fingerprint does not match provided key(s)")
	}

	switch key := key.(type) {
	case *rsa.PublicKey:
		return rsa.VerifyPSS(key, crypto.SHA256, internal.SHA256(wasm), payload.Signature, nil)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, internal.SHA256(wasm), payload.Signature) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	case ed25519.PublicKey:
		if !ed25519.Verify(key, internal.SHA256(wasm), payload.Signature) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	default:
		return fmt.Errorf("unsupported key")
	}
}

type signaturePayload struct {
	Fingerprint string
	Signature   []byte
}

func SignModule(key any, wasm []byte) ([]byte, error) {
	fingerprint, err := PublicFingerprint(key)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate fingerprint of public key: %w", err)
	}

	wasm, signature := module.WithoutCustomSection(wasm, module.PrefixSchematics+moduleSignatureKey)
	if len(signature) > 0 {
		return nil, fmt.Errorf("module is already signed")
	}

	signature, err = func() ([]byte, error) {
		switch key := key.(type) {
		case *rsa.PrivateKey:
			return rsa.SignPSS(rand.Reader, key, crypto.SHA256, internal.SHA256(wasm), nil)
		case *ecdsa.PrivateKey:
			return ecdsa.SignASN1(rand.Reader, key, internal.SHA256(wasm))
		case ed25519.PrivateKey:
			return ed25519.Sign(key, internal.SHA256(wasm)), nil
		default:
			return nil, fmt.Errorf("unsupported key")
		}
	}()
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(signaturePayload{
		Fingerprint: fingerprint,
		Signature:   signature,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal signature payload: %w", err)
	}

	return module.WithCustomSectionData(wasm, "signature", data), nil
}

func PublicFingerprint(key any) (string, error) {
	key, err := toPublicKey(key)
	if err != nil {
		return "", err
	}

	data, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", fmt.Errorf("failed to marshal key: %w", err)
	}

	var pubKey struct {
		Algo      pkix.AlgorithmIdentifier
		BitString asn1.BitString
	}
	if _, err := asn1.Unmarshal(data, &pubKey); err != nil {
		return "", fmt.Errorf("failed to decode asn1 bit string: %w", err)
	}

	return internal.SHA256HexString(pubKey.BitString.Bytes), nil
}

func toPublicKey(key crypto.PrivateKey) (crypto.PublicKey, error) {
	switch key := key.(type) {
	case *rsa.PrivateKey:
		return new(key.PublicKey), nil
	case *ecdsa.PrivateKey:
		return new(key.PublicKey), nil
	case ed25519.PrivateKey:
		return key.Public(), nil
	case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported key type")
	}
}

type PublicKeySet map[string]crypto.PublicKey

func (keyset *PublicKeySet) UnmarshalJSON(data []byte) error {
	var pems []string
	if err := json.Unmarshal(data, &pems); err != nil {
		return err
	}

	result := make(PublicKeySet, len(pems))
	for i, pem := range pems {
		key, err := ParsePublicKeyFromPEM([]byte(pem))
		if err != nil {
			return fmt.Errorf("failed to parse key at position %d: %w", i, err)
		}
		fingerprint, err := PublicFingerprint(key)
		if err != nil {
			return fmt.Errorf("failed to calculate fingerprint of key at position %d: %w", i, err)
		}
		result[fingerprint] = key
	}

	*keyset = result

	return nil
}

func LoadPublicKeysFromFS(path string) (PublicKeySet, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat: %w", err)
	}

	if !stat.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
		key, err := ParsePublicKeyFromPEM(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key from pem: %w", err)
		}
		fingerprint, err := PublicFingerprint(key)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate fingerprint from key: %w", err)
		}
		return PublicKeySet{fingerprint: key}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	keys := PublicKeySet{}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) != ".pem" {
			continue
		}
		dirKeys, err := LoadPublicKeysFromFS(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		maps.Copy(keys, dirKeys)
	}

	return keys, nil
}
