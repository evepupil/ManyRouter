package credential

import "github.com/google/uuid"

type Purpose string

const (
	PurposeNewAPIAdmin    Purpose = "new_api_admin"
	PurposeSupplierAPIKey Purpose = "supplier_api_key"
)

type Record struct {
	ID         uuid.UUID
	Purpose    Purpose
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int32
}
