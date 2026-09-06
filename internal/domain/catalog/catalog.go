package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

const ContractVersion = "m3-site-products-v1"
const minimumMetricSamples = 50
const maximumMetricAge = 15 * time.Minute

type ProductKind string

const (
	ProductDedicated ProductKind = "dedicated"
	ProductFixedAuto ProductKind = "fixed_auto"
)

type PriceEvidence struct {
	VersionID   uuid.UUID
	ConfirmedAt time.Time
}

type MetricEvidence struct {
	RequestCount  int64
	SuccessCount  int64
	TTFTP50Millis *int64
	TTFTP95Millis *int64
	FactsThrough  time.Time
}

type QualityEvidence struct {
	Score        *float64
	Confidence   string
	Authenticity string
}

type MetricKey struct {
	Group string
	Model string
}

type QualityKey struct {
	RelationID uuid.UUID
	Model      string
}

type BuildInput struct {
	SiteID        uuid.UUID
	SiteName      string
	RoutePlanID   uuid.UUID
	ScoreRunID    *uuid.UUID
	Plan          routing.Snapshot
	StrategyKinds map[string]string
	Prices        map[string]PriceEvidence
	Metrics       map[MetricKey]MetricEvidence
	Qualities     map[QualityKey]QualityEvidence
	Now           time.Time
}

type Product struct {
	Model              string      `json:"model"`
	Kind               ProductKind `json:"kind"`
	StrategyKind       string      `json:"strategy_kind,omitempty"`
	DisplayName        string      `json:"display_name"`
	GroupKey           string      `json:"group_key"`
	EntryOpen          bool        `json:"entry_open"`
	SaleRatio          string      `json:"sale_ratio"`
	PriceVersionID     *uuid.UUID  `json:"price_version_id,omitempty"`
	PriceConfirmedAt   *time.Time  `json:"price_confirmed_at,omitempty"`
	AvailableSuppliers int         `json:"available_suppliers"`
	FailoverReady      bool        `json:"failover_ready"`
	RequestSamples     int64       `json:"request_samples"`
	SLAPercent         *float64    `json:"sla_percent,omitempty"`
	TTFTP50Millis      *int64      `json:"ttft_p50_ms,omitempty"`
	TTFTP95Millis      *int64      `json:"ttft_p95_ms,omitempty"`
	QualityGrade       string      `json:"quality_grade"`
	Confidence         string      `json:"confidence"`
	FactsThrough       *time.Time  `json:"facts_through,omitempty"`
	Status             string      `json:"status"`
}

type Snapshot struct {
	ID              uuid.UUID  `json:"id"`
	ContractVersion string     `json:"contract_version"`
	Version         int64      `json:"version"`
	SiteID          uuid.UUID  `json:"site_id"`
	SiteName        string     `json:"site_name"`
	RoutePlanID     uuid.UUID  `json:"route_plan_id"`
	ScoreRunID      *uuid.UUID `json:"score_run_id,omitempty"`
	GeneratedAt     time.Time  `json:"generated_at"`
	FactsThrough    *time.Time `json:"facts_through,omitempty"`
	Products        []Product  `json:"products"`
	ContentHash     string     `json:"content_hash"`
}

func Build(input BuildInput) (Snapshot, error) {
	if input.SiteID == uuid.Nil || input.RoutePlanID == uuid.Nil || input.SiteName == "" || input.Now.IsZero() ||
		input.Plan.SchemaVersion != routing.SiteSnapshotSchemaVersion || input.Plan.SiteID != input.SiteID {
		return Snapshot{}, errors.New("catalog source is invalid")
	}
	products := make([]Product, 0)
	for _, resource := range input.Plan.Resources {
		if resource.Channel.DesiredStatus != routing.DesiredEnabled {
			continue
		}
		for _, model := range resource.Channel.Models {
			products = append(products, buildProduct(
				input, model.Model, ProductDedicated, "", resource.Group.DisplayName,
				resource.Group.Key, resource.Group.Visible, resource.Group.SaleRatio, []routing.Snapshot{resource},
			))
		}
	}
	for _, group := range input.Plan.AutoGroups {
		strategyKind := input.StrategyKinds[group.Key]
		if strategyKind == "" {
			strategyKind = strategyKindForGroup(group.Key)
		}
		models := make(map[string][]routing.Snapshot)
		for _, resource := range input.Plan.Resources {
			if resource.Channel.DesiredStatus != routing.DesiredEnabled || !contains(resource.Channel.ExtraGroupKeys, group.Key) {
				continue
			}
			for _, model := range resource.Channel.Models {
				models[model.Model] = append(models[model.Model], resource)
			}
		}
		modelNames := make([]string, 0, len(models))
		for model := range models {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		for _, model := range modelNames {
			products = append(products, buildProduct(
				input, model, ProductFixedAuto, strategyKind, group.DisplayName,
				group.Key, group.Visible, group.SaleRatio, models[model],
			))
		}
	}
	sort.Slice(products, func(i, j int) bool {
		if products[i].Model != products[j].Model {
			return products[i].Model < products[j].Model
		}
		if products[i].Kind != products[j].Kind {
			return products[i].Kind < products[j].Kind
		}
		if products[i].StrategyKind != products[j].StrategyKind {
			return products[i].StrategyKind < products[j].StrategyKind
		}
		return products[i].GroupKey < products[j].GroupKey
	})
	var factsThrough *time.Time
	for _, product := range products {
		if product.FactsThrough != nil && (factsThrough == nil || product.FactsThrough.After(*factsThrough)) {
			value := product.FactsThrough.UTC()
			factsThrough = &value
		}
	}
	return Snapshot{
		ContractVersion: ContractVersion, SiteID: input.SiteID, SiteName: input.SiteName,
		RoutePlanID: input.RoutePlanID, ScoreRunID: input.ScoreRunID,
		GeneratedAt: input.Now.UTC(), FactsThrough: factsThrough, Products: products,
	}, nil
}

func buildProduct(
	input BuildInput,
	model string,
	kind ProductKind,
	strategyKind string,
	displayName string,
	groupKey string,
	entryOpen bool,
	saleRatio string,
	members []routing.Snapshot,
) Product {
	product := Product{
		Model: model, Kind: kind, StrategyKind: strategyKind, DisplayName: displayName,
		GroupKey: groupKey, EntryOpen: entryOpen, SaleRatio: saleRatio,
		AvailableSuppliers: len(members), FailoverReady: len(members) >= 2,
		QualityGrade: "insufficient", Confidence: "insufficient", Status: "insufficient",
	}
	if price, ok := input.Prices[groupKey]; ok && price.VersionID != uuid.Nil && !price.ConfirmedAt.IsZero() {
		id := price.VersionID
		confirmed := price.ConfirmedAt.UTC()
		product.PriceVersionID = &id
		product.PriceConfirmedAt = &confirmed
	}
	metric, hasMetric := input.Metrics[MetricKey{Group: groupKey, Model: model}]
	if hasMetric {
		product.RequestSamples = metric.RequestCount
		product.TTFTP50Millis = metric.TTFTP50Millis
		product.TTFTP95Millis = metric.TTFTP95Millis
		if !metric.FactsThrough.IsZero() {
			facts := metric.FactsThrough.UTC()
			product.FactsThrough = &facts
		}
		if metric.RequestCount > 0 && metric.SuccessCount >= 0 && metric.SuccessCount <= metric.RequestCount {
			value := float64(metric.SuccessCount) * 100 / float64(metric.RequestCount)
			product.SLAPercent = &value
		}
	}
	quality, qualityConfidence := aggregateQuality(input.Qualities, members, model)
	product.QualityGrade = quality
	metricConfidence := "insufficient"
	if metric.RequestCount >= 200 {
		metricConfidence = "high"
	} else if metric.RequestCount >= minimumMetricSamples {
		metricConfidence = "medium"
	}
	product.Confidence = weakerConfidence(metricConfidence, qualityConfidence)
	switch {
	case !entryOpen:
		product.Status = "closed"
	case len(members) == 0:
		product.Status = "unavailable"
	case hasMetric && !metric.FactsThrough.IsZero() && input.Now.UTC().Sub(metric.FactsThrough.UTC()) > maximumMetricAge:
		product.Status = "stale"
	case metric.RequestCount < minimumMetricSamples || quality == "insufficient":
		product.Status = "insufficient"
	default:
		product.Status = "available"
	}
	return product
}

func aggregateQuality(values map[QualityKey]QualityEvidence, members []routing.Snapshot, model string) (string, string) {
	minimum := 101.0
	confidence := "high"
	for _, member := range members {
		evidence, ok := values[QualityKey{RelationID: member.RelationID, Model: model}]
		if !ok || evidence.Score == nil || evidence.Authenticity != "consistent" {
			return "insufficient", "insufficient"
		}
		if *evidence.Score < minimum {
			minimum = *evidence.Score
		}
		confidence = weakerConfidence(confidence, evidence.Confidence)
	}
	if len(members) == 0 || minimum > 100 {
		return "insufficient", "insufficient"
	}
	switch {
	case minimum >= 90:
		return "excellent", confidence
	case minimum >= 75:
		return "good", confidence
	case minimum >= 60:
		return "fair", confidence
	default:
		return "poor", confidence
	}
}

func weakerConfidence(left, right string) string {
	rank := map[string]int{"insufficient": 0, "low": 1, "medium": 2, "high": 3}
	if rank[right] < rank[left] {
		return right
	}
	return left
}

func strategyKindForGroup(groupKey string) string {
	switch groupKey {
	case "mrap":
		return "lowest_price"
	case "mral":
		return "low_latency"
	case "mras":
		return "high_sla"
	case "mraq":
		return "high_quality"
	case "mrab":
		return "balanced"
	default:
		return "custom"
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func ContentHash(snapshot Snapshot) (string, error) {
	payload, err := json.Marshal(struct {
		ContractVersion string
		SiteID          uuid.UUID
		SiteName        string
		RoutePlanID     uuid.UUID
		ScoreRunID      *uuid.UUID
		FactsThrough    *time.Time
		Products        []Product
	}{
		ContractVersion: snapshot.ContractVersion, SiteID: snapshot.SiteID, SiteName: snapshot.SiteName,
		RoutePlanID: snapshot.RoutePlanID, ScoreRunID: snapshot.ScoreRunID,
		FactsThrough: snapshot.FactsThrough, Products: snapshot.Products,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
