package measurement

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (source Source) Valid() bool {
	return source == SourceRealTraffic || source == SourceDirectProbe || source == SourceSiteProbe
}

func (cursor Cursor) Validate() error {
	if cursor.OccurredAt.IsZero() || strings.TrimSpace(cursor.SourceID) == "" || len(cursor.SourceID) > 256 {
		return errors.New("measurement cursor is invalid")
	}
	if cursor.OccurredAt.Location() != time.UTC || strings.ContainsAny(cursor.SourceID, "\x00\r\n") {
		return errors.New("measurement cursor must use UTC and a stable source ID")
	}
	return nil
}

func (attribution Attribution) Validate(source Source) error {
	switch attribution.Status {
	case AttributionMapped:
		if attribution.SupplierID == uuid.Nil || source != SourceDirectProbe && attribution.RelationID == uuid.Nil {
			return errors.New("mapped measurement attribution requires its business identities")
		}
	case AttributionPending:
		if attribution.RelationID != uuid.Nil || attribution.SupplierID != uuid.Nil {
			return errors.New("pending measurement attribution cannot claim business identities")
		}
	default:
		return errors.New("measurement attribution status is invalid")
	}
	return nil
}

func (fact ErrorFact) Validate(required bool) error {
	if !required {
		if fact != (ErrorFact{}) && (fact.Class != ErrorNone || fact.StableCode != "" || fact.Summary != "" || fact.RuleVersion != "") {
			return errors.New("successful measurement cannot contain an error")
		}
		return nil
	}
	if !validErrorClass(fact.Class) || fact.Class == ErrorNone || fact.RuleVersion != ErrorClassificationRuleVersion {
		return errors.New("failed measurement requires a classified error")
	}
	if !fact.ResolvedResponsibility().Valid() {
		return errors.New("failed measurement requires an error responsibility")
	}
	if fact.StableCode != "" && !codePattern.MatchString(fact.StableCode) || len([]rune(fact.Summary)) > maxErrorSummaryRunes || SanitizeErrorText(fact.Summary) != fact.Summary {
		return errors.New("measurement error evidence exceeds its limit")
	}
	return nil
}

func (fact RequestFact) Validate() error {
	if !hashPattern.MatchString(fact.SourceHash) || !hashPattern.MatchString(fact.RequestHash) || fact.RuleVersion != MeasurementRuleVersion {
		return errors.New("request fact identity is invalid")
	}
	if !validScope(fact.Source, fact.SiteID) || strings.TrimSpace(fact.RequestID) == "" || len(fact.RequestID) > 128 || strings.ContainsAny(fact.RequestID, "\x00\r\n") {
		return errors.New("request fact source is invalid")
	}
	if err := fact.TerminalCursor.Validate(); err != nil || fact.TerminalCursor.OccurredAt.Before(fact.OccurredAt) {
		return errors.New("request fact terminal cursor is invalid")
	}
	if strings.TrimSpace(fact.Model) == "" || len(fact.Model) > 191 || len(fact.Group) > 64 || strings.ContainsAny(fact.Model+fact.Group, "\x00\r\n") {
		return errors.New("request fact model or group is invalid")
	}
	if !validFinalResult(fact.Result) {
		return errors.New("request fact result is invalid")
	}
	if !validHTTPStatus(fact.HTTPStatus) || !validStreamCompletion(fact.StreamCompletion) || fact.FinalChannelID < 0 || fact.AttemptCount < 1 {
		return errors.New("request fact completion data is invalid")
	}
	if !fact.IsStream && fact.StreamCompletion != StreamUnknown {
		return errors.New("non-stream request cannot have a stream completion result")
	}
	if err := fact.Attribution.Validate(fact.Source); err != nil {
		return err
	}
	if err := validateDurations(fact.FirstTokenMillis, fact.TotalMillis); err != nil {
		return err
	}
	if !validDurationResolution(fact.TotalMillis, fact.DurationResolutionMillis) {
		return errors.New("request fact duration resolution is invalid")
	}
	if fact.PromptTokens < 0 || fact.CompletionTokens < 0 || fact.PromptTokens > math.MaxInt64-fact.CompletionTokens || fact.TotalTokens != fact.PromptTokens+fact.CompletionTokens {
		return errors.New("request fact token usage is invalid")
	}
	if fact.OccurredAt.IsZero() || fact.OccurredAt.Location() != time.UTC {
		return errors.New("request fact time must use UTC")
	}
	return fact.Error.Validate(fact.Result != FinalSucceeded)
}

func (fact AttemptFact) Validate() error {
	if !hashPattern.MatchString(fact.SourceHash) || !hashPattern.MatchString(fact.RequestHash) || fact.RuleVersion != MeasurementRuleVersion {
		return errors.New("attempt fact identity is invalid")
	}
	if !validScope(fact.Source, fact.SiteID) || strings.TrimSpace(fact.RequestID) == "" || strings.ContainsAny(fact.RequestID, "\x00\r\n") || fact.Ordinal < 1 || fact.ChannelID < 0 {
		return errors.New("attempt fact source is invalid")
	}
	if strings.TrimSpace(fact.Model) == "" || len(fact.Model) > 191 || strings.ContainsAny(fact.Model, "\x00\r\n") {
		return errors.New("attempt fact model is invalid")
	}
	if !validAttemptResult(fact.Result) || !validHTTPStatus(fact.HTTPStatus) || !validStreamCompletion(fact.StreamCompletion) {
		return errors.New("attempt fact result is invalid")
	}
	if !fact.IsStream && fact.StreamCompletion != StreamUnknown || fact.ProducedVisibleOutput != (fact.FirstTokenMillis != nil) {
		return errors.New("attempt output evidence is inconsistent")
	}
	if err := fact.Attribution.Validate(fact.Source); err != nil {
		return err
	}
	if err := validateDurations(fact.FirstTokenMillis, fact.TotalMillis); err != nil {
		return err
	}
	if !validDurationResolution(fact.TotalMillis, fact.DurationResolutionMillis) {
		return errors.New("attempt fact duration resolution is invalid")
	}
	if fact.OccurredAt.IsZero() || fact.OccurredAt.Location() != time.UTC {
		return errors.New("attempt fact time must use UTC")
	}
	return fact.Error.Validate(fact.Result != AttemptSucceeded)
}

func (fact QuarantineFact) Validate() error {
	if !hashPattern.MatchString(fact.SourceHash) || fact.Source != SourceRealTraffic && fact.Source != SourceSiteProbe || fact.SiteID == uuid.Nil {
		return errors.New("quarantined measurement identity is invalid")
	}
	if err := fact.Cursor.Validate(); err != nil {
		return err
	}
	if !codePattern.MatchString(fact.ReasonCode) {
		return errors.New("quarantined measurement reason is invalid")
	}
	return nil
}

func (batch Batch) Validate() error {
	if !validScope(batch.Source, batch.SiteID) {
		return errors.New("measurement batch scope is invalid")
	}
	if !batch.From.IsZero() {
		if err := batch.From.Validate(); err != nil {
			return err
		}
	}
	if !batch.Next.IsZero() {
		if err := batch.Next.Validate(); err != nil {
			return err
		}
	}
	if !batch.From.IsZero() && !batch.Next.IsZero() && batch.Next.Compare(batch.From) < 0 {
		return errors.New("measurement batch cursor moved backwards")
	}
	requests := make(map[string]RequestFact, len(batch.Requests))
	for _, request := range batch.Requests {
		if err := request.Validate(); err != nil {
			return err
		}
		if request.Source != batch.Source || request.SiteID != batch.SiteID || requests[request.RequestHash].RequestHash != "" {
			return errors.New("measurement batch contains duplicate or foreign requests")
		}
		requests[request.RequestHash] = request
	}
	ordinals := make(map[string]map[int]AttemptFact, len(requests))
	attemptSources := make(map[string]bool, len(batch.Attempts))
	for _, attempt := range batch.Attempts {
		if err := attempt.Validate(); err != nil {
			return err
		}
		request, exists := requests[attempt.RequestHash]
		if !exists || attempt.Source != batch.Source || attempt.SiteID != batch.SiteID || attempt.RequestID != request.RequestID {
			return errors.New("measurement attempt does not belong to its request")
		}
		if ordinals[attempt.RequestHash] == nil {
			ordinals[attempt.RequestHash] = make(map[int]AttemptFact)
		}
		if _, duplicate := ordinals[attempt.RequestHash][attempt.Ordinal]; duplicate {
			return errors.New("measurement attempt ordinal is duplicated")
		}
		if attemptSources[attempt.SourceHash] {
			return errors.New("measurement attempt source is duplicated")
		}
		attemptSources[attempt.SourceHash] = true
		ordinals[attempt.RequestHash][attempt.Ordinal] = attempt
	}
	for requestHash, request := range requests {
		attempts := ordinals[requestHash]
		if len(attempts) != request.AttemptCount {
			return fmt.Errorf("request %s attempt count does not match", requestHash)
		}
		for ordinal := 1; ordinal <= request.AttemptCount; ordinal++ {
			if _, exists := attempts[ordinal]; !exists {
				return errors.New("measurement attempt sequence has a gap")
			}
		}
		last := attempts[request.AttemptCount]
		if last.ChannelID != request.FinalChannelID || last.HTTPStatus != request.HTTPStatus || last.IsStream != request.IsStream || !last.IsFinal || !resultsMatch(request.Result, last.Result) {
			return errors.New("request final result does not match its final attempt")
		}
		for ordinal := 1; ordinal < request.AttemptCount; ordinal++ {
			if attempts[ordinal].IsFinal {
				return errors.New("only the last measurement attempt can be final")
			}
		}
	}
	quarantines := make(map[string]bool, len(batch.Quarantines))
	for _, quarantine := range batch.Quarantines {
		if err := quarantine.Validate(); err != nil {
			return err
		}
		if quarantine.Source != batch.Source || quarantine.SiteID != batch.SiteID || quarantines[quarantine.SourceHash] {
			return errors.New("measurement batch contains duplicate or foreign quarantines")
		}
		quarantines[quarantine.SourceHash] = true
	}
	return nil
}

func validScope(source Source, siteID uuid.UUID) bool {
	if !source.Valid() {
		return false
	}
	return source == SourceDirectProbe || siteID != uuid.Nil
}

func validateDurations(firstToken, total *int64) error {
	if firstToken != nil && *firstToken < 0 || total != nil && *total < 0 {
		return errors.New("measurement duration cannot be negative")
	}
	if firstToken != nil && total != nil && *firstToken > *total {
		return errors.New("first-token duration cannot exceed total duration")
	}
	return nil
}

func validDurationResolution(total *int64, resolution int64) bool {
	if total == nil {
		return resolution == 0
	}
	return resolution == DurationResolutionMillisecond || resolution == DurationResolutionSecond
}

func validStreamCompletion(value StreamCompletion) bool {
	return value == StreamUnknown || value == StreamCompleted || value == StreamIncomplete
}

func validHTTPStatus(status int) bool {
	return status == 0 || status >= 100 && status <= 599
}

func validErrorClass(value ErrorClass) bool {
	switch value {
	case ErrorNone, ErrorRateLimited, ErrorAuthentication, ErrorBalanceExhausted, ErrorTimeout, ErrorInvalidRequest, ErrorUpstreamUnavailable, ErrorStreamIncomplete, ErrorCancelled, ErrorRejected, ErrorUnknown:
		return true
	default:
		return false
	}
}

func validFinalResult(result FinalResult) bool {
	switch result {
	case FinalSucceeded, FinalFailed, FinalCancelled, FinalRejected, FinalIncomplete:
		return true
	default:
		return false
	}
}

func validAttemptResult(result AttemptResult) bool {
	switch result {
	case AttemptSucceeded, AttemptFailed, AttemptCancelled, AttemptRejected, AttemptIncomplete:
		return true
	default:
		return false
	}
}

func resultsMatch(request FinalResult, attempt AttemptResult) bool {
	switch request {
	case FinalSucceeded:
		return attempt == AttemptSucceeded
	case FinalCancelled:
		return attempt == AttemptCancelled
	case FinalRejected:
		return attempt == AttemptRejected
	case FinalIncomplete:
		return attempt == AttemptIncomplete
	case FinalFailed:
		return attempt == AttemptFailed
	default:
		return false
	}
}
