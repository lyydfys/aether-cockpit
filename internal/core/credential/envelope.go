// Package credential seals account credentials with a caller-provided key.
//
// v2 payload: "AE2\x01" | nonce(12) | AES-256-GCM ciphertext.
// The provider and account IDs are bound as AAD so a sealed credential cannot
// be moved to another account. v1 (simplecrypt, no magic) stays readable and is
// migrated to v2 by the account store.
package credential

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

const (
	KeySize   = 32
	nonceSize = 12
)

var magic = []byte("AE2\x01")

// AAD binds a sealed credential to its owning account.
func AAD(providerID, accountID string) string {
	return providerID + "\x00" + accountID
}

func IsV2(payload []byte) bool {
	return bytes.HasPrefix(payload, magic)
}

func Seal(key, plaintext []byte, aad string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, magic...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, []byte(aad)), nil
}

func Open(key, payload []byte, aad string) ([]byte, error) {
	if !IsV2(payload) {
		return nil, errors.New("credential: not an envelope v2 payload")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	rest := payload[len(magic):]
	if len(rest) < gcm.NonceSize() {
		return nil, errors.New("credential: envelope is truncated")
	}
	nonce, ciphertext := rest[:gcm.NonceSize()], rest[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, []byte(aad))
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, errors.New("credential: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
