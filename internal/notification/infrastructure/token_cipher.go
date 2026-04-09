package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

type AESTokenCipher struct {
	key []byte
}

func NewAESTokenCipher(hexKey string) (*AESTokenCipher, error) {
	trimmed := strings.TrimSpace(hexKey)
	if trimmed == "" {
		return nil, errors.New("whatsapp account encryption key is not configured")
	}
	key, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode whatsapp account encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("whatsapp account encryption key must be 32 bytes encoded as 64 hex characters")
	}
	return &AESTokenCipher{key: key}, nil
}

func (c *AESTokenCipher) Encrypt(token string) (string, string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", "", err
	}
	ciphertext := gcm.Seal(nil, iv, []byte(token), nil)
	return hex.EncodeToString(ciphertext), hex.EncodeToString(iv), nil
}

func (c *AESTokenCipher) Decrypt(ciphertextHex, ivHex string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv, err := hex.DecodeString(strings.TrimSpace(ivHex))
	if err != nil {
		return "", fmt.Errorf("decode whatsapp token iv: %w", err)
	}
	ciphertext, err := hex.DecodeString(strings.TrimSpace(ciphertextHex))
	if err != nil {
		return "", fmt.Errorf("decode whatsapp encrypted token: %w", err)
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
