package authn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// aeadCipher encrypts small secrets at rest (the per-operator TOTP shared
// secret) with AES-256-GCM. The 256-bit key comes from Config — in
// production a KMS-protected secret loaded at startup, the same handling as
// the JWT signing key.
type aeadCipher struct {
	aead cipher.AEAD
}

// newCipher builds an aeadCipher from a 32-byte (AES-256) key.
func newCipher(key []byte) (*aeadCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("totp encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &aeadCipher{aead: aead}, nil
}

// Encrypt returns nonce-prefixed AES-256-GCM ciphertext. Each call uses a
// fresh random nonce, so encrypting the same plaintext twice differs.
func (c *aeadCipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, reading the nonce from the ciphertext prefix.
func (c *aeadCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext shorter than nonce")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return plaintext, nil
}
