package measurement

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ProbeInput struct {
	RunID            uuid.UUID
	SampleKey        string
	Source           Source
	SiteID           uuid.UUID
	RelationID       uuid.UUID
	SupplierID       uuid.UUID
	Model            string
	Succeeded        bool
	HTTPStatus       int
	StableErrorCode  string
	ErrorText        string
	IsStream         bool
	StreamCompleted  *bool
	FirstTokenMillis *int64
	TotalMillis      *int64
	PromptTokens     int64
	CompletionTokens int64
	OccurredAt       time.Time
}

func NewProbeFacts(input ProbeInput) (RequestFact, AttemptFact, error) {
	if input.RunID == uuid.Nil || strings.TrimSpace(input.SampleKey) == "" || len(input.SampleKey) > 160 {
		return RequestFact{}, AttemptFact{}, errors.New("probe measurement identity is invalid")
	}
	if input.Source != SourceDirectProbe && input.Source != SourceSiteProbe {
		return RequestFact{}, AttemptFact{}, errors.New("probe measurement source is invalid")
	}
	if input.SupplierID == uuid.Nil || input.Source == SourceSiteProbe && (input.SiteID == uuid.Nil || input.RelationID == uuid.Nil) {
		return RequestFact{}, AttemptFact{}, errors.New("probe measurement attribution is invalid")
	}
	sourceHash := stableHash("probe", input.RunID.String(), input.SampleKey)
	requestHash := stableHash("probe_request", input.RunID.String(), input.SampleKey)
	requestID := "probe:" + requestHash[:32]
	var result FinalResult
	var attemptResult AttemptResult
	errorFact := ClassifyError(input.StableErrorCode, input.HTTPStatus, input.ErrorText)
	if input.Succeeded {
		result = FinalSucceeded
		attemptResult = AttemptSucceeded
		errorFact = ErrorFact{}
	} else {
		result = finalResultFromError(errorFact)
		attemptResult = attemptResultFromError(errorFact)
	}
	completion := streamCompletion(input.IsStream, input.StreamCompleted)
	attribution := Attribution{
		Status: AttributionMapped, RelationID: input.RelationID, SupplierID: input.SupplierID,
	}
	request := RequestFact{
		SourceHash: sourceHash, RequestHash: requestHash, RuleVersion: MeasurementRuleVersion,
		Source: input.Source, SiteID: input.SiteID, RequestID: requestID,
		TerminalCursor: Cursor{OccurredAt: input.OccurredAt.UTC(), SourceID: sourceHash}, Model: input.Model,
		Result: result, HTTPStatus: input.HTTPStatus, IsStream: input.IsStream,
		StreamCompletion: completion, Attribution: attribution,
		FirstTokenMillis: copyInt64(input.FirstTokenMillis), TotalMillis: copyInt64(input.TotalMillis),
		DurationResolutionMillis: durationResolution(input.TotalMillis, DurationResolutionMillisecond),
		PromptTokens:             input.PromptTokens, CompletionTokens: input.CompletionTokens,
		TotalTokens: input.PromptTokens + input.CompletionTokens, AttemptCount: 1,
		OccurredAt: input.OccurredAt.UTC(), Error: errorFact,
	}
	attempt := AttemptFact{
		SourceHash: stableHash(sourceHash, "attempt", "1"), RequestHash: requestHash,
		RuleVersion: MeasurementRuleVersion, Source: input.Source, SiteID: input.SiteID,
		RequestID: requestID, Ordinal: 1, Model: input.Model, Attribution: attribution,
		Result: attemptResult, HTTPStatus: input.HTTPStatus, IsFinal: true, IsStream: input.IsStream,
		ProducedVisibleOutput: input.FirstTokenMillis != nil, StreamCompletion: completion,
		FirstTokenMillis: copyInt64(input.FirstTokenMillis), TotalMillis: copyInt64(input.TotalMillis),
		DurationResolutionMillis: durationResolution(input.TotalMillis, DurationResolutionMillisecond),
		OccurredAt:               input.OccurredAt.UTC(), Error: errorFact,
	}
	if err := request.Validate(); err != nil {
		return RequestFact{}, AttemptFact{}, err
	}
	if err := attempt.Validate(); err != nil {
		return RequestFact{}, AttemptFact{}, err
	}
	return request, attempt, nil
}

func durationResolution(total *int64, resolution int64) int64 {
	if total == nil {
		return 0
	}
	return resolution
}
