package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Box struct{ key []byte }

func LoadOrCreate(path string) (*Box, error) {
	if path == "" {
		return nil, errors.New("master-key path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create master-key directory: %w", err)
	}
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create master key: %w", err)
		}
		if _, err := file.Write(key); err != nil {
			file.Close()
			return nil, fmt.Errorf("write master key: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close master key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("protect master key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("master key must be exactly 32 bytes")
	}
	return &Box{key: key}, nil
}

func (b *Box) Seal(plaintext string) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, []byte(plaintext), nil), nonce, nil
}

func (b *Box) Open(ciphertext, nonce []byte) (string, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("could not decrypt provider credential")
	}
	return string(plaintext), nil
}
