package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/evepupil/ManyRouter/internal/domain/value"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const SnapshotSchemaVersion = 1

type ModelRoute struct {
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
}

type DesiredGroup struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	SaleRatio   string `json:"sale_ratio"`
	Visible     bool   `json:"visible"`
}

type DesiredChannel struct {
	ID                uuid.UUID     `json:"id"`
	ManagedTag        string        `json:"managed_tag"`
	Name              string        `json:"name"`
	Protocol          string        `json:"protocol"`
	BaseURL           string        `json:"base_url"`
	CredentialID      uuid.UUID     `json:"credential_id"`
	CredentialVersion int32         `json:"credential_version"`
	Models            []ModelRoute  `json:"models"`
	GroupKey          string        `json:"group_key"`
	Priority          int64         `json:"priority"`
	Weight            int           `json:"weight"`
	DesiredStatus     DesiredStatus `json:"desired_status"`
}

type Snapshot struct {
	SchemaVersion int            `json:"schema_version"`
	SiteID        uuid.UUID      `json:"site_id"`
	RelationID    uuid.UUID      `json:"relation_id"`
	SupplierID    uuid.UUID      `json:"supplier_id"`
	Group         DesiredGroup   `json:"group"`
	Channel       DesiredChannel `json:"channel"`
}

func BuildSnapshot(siteData site.Site, supplierData supplier.Supplier, relation Relation, channel ManagedChannel) (Snapshot, error) {
	if err := siteData.CanSync(); err != nil {
		return Snapshot{}, err
	}
	if err := supplierData.CanDeploy(); err != nil {
		return Snapshot{}, err
	}
	if relation.SiteID != siteData.ID || relation.SupplierID != supplierData.ID {
		return Snapshot{}, errors.New("relation does not belong to the supplied site and supplier")
	}
	if channel.RelationID != relation.ID {
		return Snapshot{}, errors.New("managed channel does not belong to the relation")
	}

	models := make([]ModelRoute, 0, len(supplierData.Models))
	for _, model := range supplierData.Models {
		if !model.Enabled {
			continue
		}
		models = append(models, ModelRoute{Model: model.Name, UpstreamModel: model.UpstreamName})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
	if len(models) == 0 {
		return Snapshot{}, errors.New("snapshot requires at least one enabled model")
	}

	return Snapshot{
		SchemaVersion: SnapshotSchemaVersion,
		SiteID:        siteData.ID,
		RelationID:    relation.ID,
		SupplierID:    supplierData.ID,
		Group: DesiredGroup{
			Key:         relation.GroupKey,
			DisplayName: relation.GroupDisplayName,
			SaleRatio:   relation.SaleRatio.String(),
			Visible:     relation.Visible,
		},
		Channel: DesiredChannel{
			ID:                channel.ID,
			ManagedTag:        channel.ManagedTag,
			Name:              supplierData.Name + " [ManyRouter]",
			Protocol:          string(supplierData.Protocol),
			BaseURL:           supplierData.UpstreamBaseURL,
			CredentialID:      supplierData.CredentialID,
			CredentialVersion: supplierData.CredentialVersion,
			Models:            models,
			GroupKey:          relation.GroupKey,
			Priority:          0,
			Weight:            100,
			DesiredStatus:     DesiredEnabled,
		},
	}, nil
}

func EncodeSnapshot(snapshot Snapshot) ([]byte, string, error) {
	if err := ValidateSnapshot(snapshot); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func DecodeSnapshot(payload []byte) (Snapshot, error) {
	var snapshot Snapshot
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, err
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func ValidateSnapshot(snapshot Snapshot) error {
	if snapshot.SchemaVersion != SnapshotSchemaVersion {
		return errors.New("unsupported snapshot schema version")
	}
	if snapshot.SiteID == uuid.Nil || snapshot.RelationID == uuid.Nil || snapshot.SupplierID == uuid.Nil {
		return errors.New("snapshot site, relation, and supplier IDs are required")
	}
	if snapshot.Group.Key != GroupKey(snapshot.RelationID) || snapshot.Channel.GroupKey != snapshot.Group.Key {
		return errors.New("snapshot managed group identity is invalid")
	}
	if snapshot.Channel.ID == uuid.Nil || snapshot.Channel.ManagedTag != ManagedTag(snapshot.RelationID) {
		return errors.New("snapshot managed channel identity is invalid")
	}
	if snapshot.Channel.CredentialID == uuid.Nil || snapshot.Channel.CredentialVersion <= 0 {
		return errors.New("snapshot channel credential is invalid")
	}
	if snapshot.Channel.Protocol != string(supplier.ProtocolOpenAICompatible) {
		return errors.New("snapshot channel protocol is unsupported")
	}
	normalizedURL, err := value.NormalizeHTTPBaseURL(snapshot.Channel.BaseURL)
	if err != nil || normalizedURL != snapshot.Channel.BaseURL {
		return errors.New("snapshot channel base URL is invalid")
	}
	ratio, err := decimal.NewFromString(snapshot.Group.SaleRatio)
	if err != nil || !ratio.IsPositive() || ratio.Exponent() < -6 || ratio.GreaterThan(decimal.RequireFromString("999999.999999")) {
		return errors.New("snapshot sale ratio is invalid")
	}
	if len(snapshot.Channel.Models) == 0 {
		return errors.New("snapshot requires at least one model")
	}
	previous := ""
	for _, model := range snapshot.Channel.Models {
		if strings.TrimSpace(model.Model) == "" || strings.TrimSpace(model.UpstreamModel) == "" || strings.ContainsAny(model.Model+model.UpstreamModel, ",\r\n") {
			return errors.New("snapshot contains an invalid model")
		}
		if previous != "" && model.Model <= previous {
			return errors.New("snapshot models must be unique and sorted")
		}
		previous = model.Model
	}
	if snapshot.Channel.Weight < 0 {
		return errors.New("snapshot channel weight is invalid")
	}
	if snapshot.Channel.DesiredStatus != DesiredEnabled && snapshot.Channel.DesiredStatus != DesiredDisabled {
		return errors.New("snapshot desired channel status is invalid")
	}
	return nil
}
