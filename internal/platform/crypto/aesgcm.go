package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/google/uuid"
)

const MasterKeySize = 32

type Vault struct {
	aead       cipher.AEAD
	keyVersion int32
	random     io.Reader
}

func NewVault(masterKey []byte, keyVersion int32) (*Vault, error) {
	if len(masterKey) != MasterKeySize {
		return nil, fmt.Errorf("master key must contain %d bytes", MasterKeySize)
	}
	if keyVersion <= 0 {
		return nil, errors.New("key version must be positive")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead, keyVersion: keyVersion, random: cryptorand.Reader}, nil
}

func NewVaultFromBase64(encoded string, keyVersion int32) (*Vault, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	defer clear(key)
	return NewVault(key, keyVersion)
}

func (v *Vault) Encrypt(credentialID uuid.UUID, purpose credential.Purpose, plaintext []byte) (credential.Record, error) {
	if credentialID == uuid.Nil {
		return credential.Record{}, errors.New("credential ID is required")
	}
	if purpose == "" {
		return credential.Record{}, errors.New("credential purpose is required")
	}
	if len(plaintext) == 0 {
		return credential.Record{}, errors.New("credential value is required")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(v.random, nonce); err != nil {
		return credential.Record{}, fmt.Errorf("create credential nonce: %w", err)
	}
	aad := additionalData(credentialID, string(purpose), v.keyVersion)
	ciphertext := v.aead.Seal(nil, nonce, plaintext, aad)
	return credential.Record{
		ID:         credentialID,
		Purpose:    purpose,
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyVersion: v.keyVersion,
	}, nil
}

func (v *Vault) Decrypt(envelope credential.Record) ([]byte, error) {
	if envelope.KeyVersion != v.keyVersion {
		return nil, fmt.Errorf("credential key version %d is unavailable", envelope.KeyVersion)
	}
	if len(envelope.Nonce) != v.aead.NonceSize() {
		return nil, errors.New("credential nonce has invalid length")
	}
	aad := additionalData(envelope.ID, string(envelope.Purpose), envelope.KeyVersion)
	plaintext, err := v.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, errors.New("credential integrity check failed")
	}
	return plaintext, nil
}

func additionalData(credentialID uuid.UUID, purpose string, keyVersion int32) []byte {
	return []byte(fmt.Sprintf("%s|%s|%d", credentialID, purpose, keyVersion))
}
