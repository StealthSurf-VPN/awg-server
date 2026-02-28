package awg

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

func GeneratePrivateKey() ([32]byte, error) {
	var key [32]byte

	_, err := rand.Read(key[:])
	if err != nil {
		return key, fmt.Errorf("generate private key: %w", err)
	}

	key[0] &= 248
	key[31] &= 127
	key[31] |= 64

	return key, nil
}

func PublicKeyFromPrivate(private [32]byte) [32]byte {
	var public [32]byte

	curve25519.ScalarBaseMult(&public, &private)

	return public
}

func KeyToBase64(key [32]byte) string {
	return base64.StdEncoding.EncodeToString(key[:])
}

func Base64ToKey(s string) ([32]byte, error) {
	var key [32]byte

	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return key, fmt.Errorf("decode base64 key: %w", err)
	}

	if len(b) != 32 {
		return key, fmt.Errorf("invalid key length: %d", len(b))
	}

	copy(key[:], b)

	return key, nil
}

func KeyToHex(key [32]byte) string {
	return hex.EncodeToString(key[:])
}

func HexToKey(s string) ([32]byte, error) {
	var key [32]byte

	b, err := hex.DecodeString(s)
	if err != nil {
		return key, fmt.Errorf("decode hex key: %w", err)
	}

	if len(b) != 32 {
		return key, fmt.Errorf("invalid key length: %d", len(b))
	}

	copy(key[:], b)

	return key, nil
}
