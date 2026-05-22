package authn

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func TestCipherRoundTrip(t *testing.T) {
	c, err := newCipher(testKey())
	if err != nil {
		t.Fatalf("newCipher: %v", err)
	}

	plaintext := []byte("JBSWY3DPEHPK3PXP")
	ciphertext, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Contains(ciphertext, plaintext) {
		t.Errorf("ciphertext still contains the raw plaintext")
	}

	got, err := c.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip: got %q want %q", got, plaintext)
	}
}
