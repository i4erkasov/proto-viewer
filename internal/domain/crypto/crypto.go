package crypto

// StringEncryptor is a domain port for encrypting/decrypting small secrets (passwords/tokens).
//
// Infrastructure should provide an implementation suitable for the current OS.
// Domain/business code should depend on this interface, not on concrete crypto primitives.
type StringEncryptor interface {
	EncryptString(plaintext string) (string, error)
	DecryptString(ciphertext string) (string, error)
}
