package evaluation

import (
	"time"

	"github.com/google/uuid"
)

const (
	ProtocolSingleTokenJSDV1 = "single_token_jsd_v1"
	AuthenticityPolicyV1     = "authenticity_v1"
)

type CellID string

const (
	CellNumber100EN CellID = "number_1_100:en"
	CellNumber100ZH CellID = "number_1_100:zh"
	CellNumber10EN  CellID = "number_1_10:en"
	CellNumber10ZH  CellID = "number_1_10:zh"
	CellColorEN     CellID = "color:en"
	CellColorZH     CellID = "color:zh"
	CellCoinEN      CellID = "coin:en"
	CellCoinZH      CellID = "coin:zh"
)

var protocolV1Cells = [...]CellID{
	CellNumber100EN,
	CellNumber100ZH,
	CellNumber10EN,
	CellNumber10ZH,
	CellColorEN,
	CellColorZH,
	CellCoinEN,
	CellCoinZH,
}

func RequiredCells() []CellID {
	cells := make([]CellID, len(protocolV1Cells))
	copy(cells, protocolV1Cells[:])
	return cells
}

type Distribution struct {
	Counts         map[string]uint64
	InvalidSamples uint64
}

func (d Distribution) ValidSamples() uint64 {
	var total uint64
	for _, count := range d.Counts {
		total += count
	}
	return total
}

type Stability struct {
	Measured bool
	Distance float64
}

type Fingerprint struct {
	RunID           string
	Seed            uint64
	ProtocolVersion string
	CollectedAt     time.Time
	Cells           map[CellID]Distribution
	Stability       Stability
}

type ModelSubject struct {
	SupplierID uuid.UUID
	Model      string
}

type ReferenceTrust string

const (
	ReferenceOfficial        ReferenceTrust = "official"
	ReferenceOperatorTrusted ReferenceTrust = "operator_trusted"
	ReferenceCommunity       ReferenceTrust = "community"
)

type ModelReference struct {
	ID          string
	Revision    int64
	Trust       ReferenceTrust
	Source      ModelSubject
	ExpiresAt   time.Time
	Fingerprint Fingerprint
}

func (r ModelReference) CanEstablishAuthenticity() bool {
	return r.Trust == ReferenceOfficial || r.Trust == ReferenceOperatorTrusted
}

type Verdict string

const (
	VerdictInsufficient Verdict = "insufficient"
	VerdictConsistent   Verdict = "consistent"
	VerdictSuspicious   Verdict = "suspicious"
	VerdictInconsistent Verdict = "inconsistent"
)

type Confidence string

const (
	ConfidenceNone   Confidence = "none"
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type Reason string

const (
	ReasonNoTrustedReference          Reason = "no_trusted_reference"
	ReasonReferenceExpired            Reason = "reference_expired"
	ReasonProtocolMismatch            Reason = "protocol_mismatch"
	ReasonSelfCheckMissing            Reason = "self_check_missing"
	ReasonSelfUnstable                Reason = "self_unstable"
	ReasonInsufficientSamples         Reason = "insufficient_samples"
	ReasonDistanceMatch               Reason = "distance_match"
	ReasonDistanceUncertain           Reason = "distance_uncertain"
	ReasonDistanceMismatchUnconfirmed Reason = "distance_mismatch_unconfirmed"
	ReasonDistanceMismatchConfirmed   Reason = "distance_mismatch_confirmed"
)

type FingerprintComparison struct {
	Comparable      bool
	Reason          Reason
	MeanDistance    float64
	ComparableCells int
	CellDistances   map[CellID]float64
}

type MismatchEvidence struct {
	Subject           ModelSubject
	TargetRunID       string
	TargetSeed        uint64
	ObservedAt        time.Time
	ReferenceID       string
	ReferenceRevision int64
	ProtocolVersion   string
	Distance          float64
	SelfDistance      float64
}

type AuthenticityPolicy struct {
	Version                   string
	MinimumSamplesPerCell     uint64
	MatchMaximum              float64
	UncertainMaximum          float64
	MaximumSelfDistance       float64
	MismatchConfirmationDelay time.Duration
}

func DefaultAuthenticityPolicy() AuthenticityPolicy {
	return AuthenticityPolicy{
		Version:                   AuthenticityPolicyV1,
		MinimumSamplesPerCell:     10,
		MatchMaximum:              0.25,
		UncertainMaximum:          0.35,
		MaximumSelfDistance:       0.35,
		MismatchConfirmationDelay: 30 * time.Minute,
	}
}

type AssessmentInput struct {
	Subject       ModelSubject
	Target        Fingerprint
	Reference     *ModelReference
	PriorMismatch *MismatchEvidence
	AssessedAt    time.Time
}

type AuthenticityAssessment struct {
	PolicyVersion string
	Verdict       Verdict
	Confidence    Confidence
	Reason        Reason
	AssessedAt    time.Time
	Comparison    FingerprintComparison
}
