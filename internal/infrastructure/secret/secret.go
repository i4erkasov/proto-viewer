package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// DeriveKey turns an arbitrary string into a 32-byte AES key.
func DeriveKey(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	b := make([]byte, 32)
	copy(b, sum[:])
	return b
}

// EncryptString encrypts plaintext with AES-GCM and returns base64(nonce||ciphertext).
func EncryptString(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	buf := append(nonce, ct...)
	return base64.StdEncoding.EncodeToString(buf), nil
}

// DecryptString decrypts a string created by EncryptString.
func DecryptString(key []byte, b64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce := data[:ns]
	ct := data[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// AESGCMStringEncryptor keeps the key and implements a simple encrypt/decrypt interface.
//
// This implementation is cross-platform (macOS/Windows/Linux) because it uses Go stdlib only.
// Proper key management (env var, OS keychain, etc.) should be handled at composition root.
type AESGCMStringEncryptor struct {
	key []byte
}

func NewAESGCMStringEncryptor(key []byte) *AESGCMStringEncryptor {
	b := make([]byte, len(key))
	copy(b, key)
	return &AESGCMStringEncryptor{key: b}
}

func (e *AESGCMStringEncryptor) EncryptString(plaintext string) (string, error) {
	return EncryptString(e.key, plaintext)
}

func (e *AESGCMStringEncryptor) DecryptString(ciphertext string) (string, error) {
	return DecryptString(e.key, ciphertext)
}
