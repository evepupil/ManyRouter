package routing

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type StrategyReference struct {
	ID      uuid.UUID `json:"id"`
	Version int64     `json:"version"`
}

func (channel DesiredChannel) GroupKeys() []string {
	groups := append([]string{channel.GroupKey}, channel.ExtraGroupKeys...)
	slices.Sort(groups)
	return slices.Compact(groups)
}

func (snapshot Snapshot) ResourceSnapshots() []Snapshot {
	if snapshot.SchemaVersion == SiteSnapshotSchemaVersion {
		return snapshot.Resources
	}
	return []Snapshot{snapshot}
}

func (snapshot Snapshot) Groups() []DesiredGroup {
	groups := make([]DesiredGroup, 0, len(snapshot.Resources)+len(snapshot.AutoGroups)+1)
	for _, resource := range snapshot.ResourceSnapshots() {
		groups = append(groups, resource.Group)
	}
	return append(groups, snapshot.AutoGroups...)
}

func validateSiteSnapshot(snapshot Snapshot) error {
	if snapshot.SiteID == uuid.Nil || len(snapshot.Resources) == 0 {
		return errors.New("site snapshot requires a site and resources")
	}
	if _, err := hex.DecodeString(snapshot.BillingBasisHash); err != nil || len(snapshot.BillingBasisHash) != 64 {
		return errors.New("site snapshot requires a billing basis hash")
	}
	groups := make(map[string]bool)
	for _, group := range snapshot.AutoGroups {
		ratio, err := decimal.NewFromString(group.SaleRatio)
		if !strings.HasPrefix(group.Key, "mr_a_") || strings.ContainsAny(group.Key, ",\r\n") || group.DisplayName == "" || groups[group.Key] || err != nil || !ratio.IsPositive() || ratio.Exponent() < -6 {
			return errors.New("site snapshot contains an invalid Auto group")
		}
		groups[group.Key] = true
	}
	relations := make(map[uuid.UUID]bool)
	channels := make(map[uuid.UUID]bool)
	previous := ""
	for _, resource := range snapshot.Resources {
		if resource.SchemaVersion != SnapshotSchemaVersion || resource.SiteID != snapshot.SiteID {
			return errors.New("site resources must be single-resource snapshots belonging to the site")
		}
		if err := ValidateSnapshot(resource); err != nil {
			return err
		}
		if relations[resource.RelationID] || channels[resource.Channel.ID] || resource.RelationID.String() <= previous {
			return errors.New("site resources must have unique identities and be sorted by relation")
		}
		relations[resource.RelationID], channels[resource.Channel.ID] = true, true
		previous = resource.RelationID.String()
		for _, group := range resource.Channel.ExtraGroupKeys {
			if !groups[group] {
				return errors.New("channel references an Auto group outside the site snapshot")
			}
		}
	}
	if snapshot.RelationID != snapshot.Resources[0].RelationID || snapshot.SupplierID != snapshot.Resources[0].SupplierID {
		return errors.New("site snapshot representative must match its first resource")
	}
	seenResume := make(map[uuid.UUID]bool)
	for _, id := range snapshot.ResumeRelationIDs {
		if !relations[id] || seenResume[id] {
			return errors.New("resume instruction must identify a unique site relation")
		}
		seenResume[id] = true
	}
	seenPrice := make(map[uuid.UUID]bool)
	for _, id := range snapshot.PriceVersionIDs {
		if id == uuid.Nil || seenPrice[id] {
			return errors.New("price versions must have unique nonempty identities")
		}
		seenPrice[id] = true
	}
	previousStrategy := ""
	for _, reference := range snapshot.StrategyVersions {
		if reference.ID == uuid.Nil || reference.Version < 1 || reference.ID.String() <= previousStrategy {
			return errors.New("strategy versions must have nonempty unique identities, positive versions and be sorted")
		}
		previousStrategy = reference.ID.String()
	}
	return nil
}
