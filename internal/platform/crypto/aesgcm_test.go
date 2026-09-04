package crypto_test

import (
	"bytes"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
)

func TestVaultBindsCiphertextToPurposeAndIdentity(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x42}, platformcrypto.MasterKeySize)
	vault, err := platformcrypto.NewVault(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	envelope, err := vault.Encrypt(id, credential.PurposeSupplierAPIKey, []byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := vault.Decrypt(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret-value" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	tampered := envelope
	tampered.Purpose = credential.PurposeNewAPIAdmin
	if _, err := vault.Decrypt(tampered); err == nil {
		t.Fatal("decrypt accepted a different credential purpose")
	}
	tampered = envelope
	tampered.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	tampered.Ciphertext[0] ^= 0xff
	if _, err := vault.Decrypt(tampered); err == nil {
		t.Fatal("decrypt accepted modified ciphertext")
	}
}

func TestNewVaultRejectsWrongKeyLength(t *testing.T) {
	t.Parallel()
	if _, err := platformcrypto.NewVault([]byte("short"), 1); err == nil {
		t.Fatal("expected invalid key length to fail")
	}
}
