// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
// Package quazalcrypto implements the legacy authentication containers used by
// TDU's Quazal client. These formats are for game compatibility, not new systems.
package quazalcrypto

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rc4"
	"errors"
)

const (
	KeySize           = 16
	TagSize           = md5.Size
	InitialIterations = 65000
)

// Key is the raw MD5-derived or service-session key, not its hexadecimal text.
type Key [KeySize]byte

var (
	ErrAuthentication = errors.New("quazal: invalid authenticated container")
	ErrLength         = errors.New("quazal: invalid encoded length")
	ErrChallenge      = errors.New("quazal: challenge response mismatch")
	ErrPrincipal      = errors.New("quazal: challenge principal does not match ticket")
)

// InitialKey is the signup hash: 65000 MD5 operations starting with the exact
// password bytes. Callers must supply the C-string content, excluding its NUL.
func InitialKey(password []byte) Key {
	key := Key(md5.Sum(password))
	for i := 1; i < InitialIterations; i++ {
		key = Key(md5.Sum(key[:]))
	}
	return key
}

// ExtendInitial converts a stored signup hash into the PID-specific login key.
func ExtendInitial(initial Key, pid uint32) Key {
	for i := uint32(0); i < pid%1024; i++ {
		initial = Key(md5.Sum(initial[:]))
	}
	return initial
}

// PasswordKey performs 65000+(pid%1024) MD5 operations, matching 0x1ec0a0.
func PasswordKey(password []byte, pid uint32) Key {
	return ExtendInitial(InitialKey(password), pid)
}

// Seal encrypts with a fresh RC4 state, then appends HMAC-MD5 over ciphertext.
// The retail RC4 transform resets its working S-box before each invocation.
// It does not add a Buffer length prefix. Empty plaintext is permitted.
func Seal(key Key, plaintext []byte) []byte {
	sealed := make([]byte, len(plaintext)+TagSize)
	cipher, _ := rc4.NewCipher(key[:]) // Key's fixed 16 bytes are always valid.
	cipher.XORKeyStream(sealed[:len(plaintext)], plaintext)
	mac := hmac.New(md5.New, key[:])
	_, _ = mac.Write(sealed[:len(plaintext)])
	copy(sealed[len(plaintext):], mac.Sum(nil))
	return sealed
}

// Open verifies the complete tag before decrypting. Neither input nor returned
// plaintext shares writable storage; authentication failure returns no plaintext.
func Open(key Key, sealed []byte) ([]byte, error) {
	if len(sealed) < TagSize {
		return nil, ErrAuthentication
	}
	ciphertext := sealed[:len(sealed)-TagSize]
	mac := hmac.New(md5.New, key[:])
	_, _ = mac.Write(ciphertext)
	if !hmac.Equal(sealed[len(ciphertext):], mac.Sum(nil)) {
		return nil, ErrAuthentication
	}
	plaintext := make([]byte, len(ciphertext))
	cipher, _ := rc4.NewCipher(key[:])
	cipher.XORKeyStream(plaintext, ciphertext)
	return plaintext, nil
}
