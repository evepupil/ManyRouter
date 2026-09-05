package measurement

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ChannelAttribution struct {
	RelationID uuid.UUID
	SupplierID uuid.UUID
}

type ChannelAttributionResolver func(int64, time.Time) Attribution

type NewAPILogInput struct {
	Cursor                   Cursor
	RequestID                string
	Result                   FinalResult
	Model                    string
	Group                    string
	CurrentChannelID         int64
	UseChannelIDs            []int64
	StableErrorCode          string
	HTTPStatus               int
	ErrorText                string
	IsStream                 bool
	StreamCompleted          *bool
	FirstTokenMillis         *int64
	TotalMillis              *int64
	DurationResolutionMillis int64
	PromptTokens             int64
	CompletionTokens         int64
	Username                 string `json:"-"`
	IP                       string `json:"-"`
	RequestBody              string `json:"-"`
	ResponseBody             string `json:"-"`
	APIKey                   string `json:"-"`
}

func ConvertNewAPILogs(source Source, siteID uuid.UUID, from Cursor, inputs []NewAPILogInput, channels map[int64]ChannelAttribution) (Batch, error) {
	return ConvertNewAPILogsWithResolver(source, siteID, from, inputs, func(channelID int64, _ time.Time) Attribution {
		value, exists := channels[channelID]
		if !exists || value.RelationID == uuid.Nil || value.SupplierID == uuid.Nil {
			return Attribution{Status: AttributionPending}
		}
		return Attribution{Status: AttributionMapped, RelationID: value.RelationID, SupplierID: value.SupplierID}
	})
}

func ConvertNewAPILogsWithResolver(source Source, siteID uuid.UUID, from Cursor, inputs []NewAPILogInput, resolve ChannelAttributionResolver) (Batch, error) {
	batch := Batch{
		Source: source, SiteID: siteID, From: normalizeCursor(from), Next: normalizeCursor(from),
		Requests: make([]RequestFact, 0), Attempts: make([]AttemptFact, 0), Quarantines: make([]QuarantineFact, 0),
	}
	if (source != SourceRealTraffic && source != SourceSiteProbe) || siteID == uuid.Nil {
		return Batch{}, errors.New("new API measurement scope is invalid")
	}
	if !batch.From.IsZero() {
		if err := batch.From.Validate(); err != nil {
			return Batch{}, err
		}
	}
	records := append([]NewAPILogInput(nil), inputs...)
	for index := range records {
		records[index].Cursor = normalizeCursor(records[index].Cursor)
		if err := records[index].Cursor.Validate(); err != nil {
			return Batch{}, err
		}
	}
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].Cursor.Compare(records[right].Cursor) < 0
	})
	seenSources := make(map[string]bool, len(records))
	grouped := make(map[string][]NewAPILogInput)
	order := make([]string, 0)
	for _, record := range records {
		sourceHash := newAPILogSourceHash(source, siteID, record)
		if seenSources[sourceHash] {
			continue
		}
		seenSources[sourceHash] = true
		if reason := invalidNewAPILogReason(record); reason != "" {
			quarantine, err := NewQuarantineFact(source, siteID, record.Cursor, reason)
			if err != nil {
				return Batch{}, err
			}
			batch.Quarantines = append(batch.Quarantines, quarantine)
			if batch.Next.IsZero() || record.Cursor.Compare(batch.Next) > 0 {
				batch.Next = record.Cursor
			}
			continue
		}
		if len(grouped[record.RequestID]) == 0 {
			order = append(order, record.RequestID)
		}
		grouped[record.RequestID] = append(grouped[record.RequestID], record)
		if batch.Next.IsZero() || record.Cursor.Compare(batch.Next) > 0 {
			batch.Next = record.Cursor
		}
	}
	for _, requestID := range order {
		request, attempts := convertNewAPIRequest(source, siteID, grouped[requestID], resolve)
		batch.Requests = append(batch.Requests, request)
		batch.Attempts = append(batch.Attempts, attempts...)
	}
	if err := batch.Validate(); err != nil {
		return Batch{}, err
	}
	return batch, nil
}

func invalidNewAPILogReason(input NewAPILogInput) string {
	if strings.TrimSpace(input.RequestID) == "" || len(input.RequestID) > 128 || strings.ContainsAny(input.RequestID, "\x00\r\n") {
		return "invalid_request_id"
	}
	if input.Result != FinalSucceeded && input.Result != FinalFailed {
		return "invalid_result"
	}
	if strings.TrimSpace(input.Model) == "" || len(input.Model) > 191 || len(input.Group) > 64 || strings.ContainsAny(input.Model+input.Group, "\x00\r\n") {
		return "invalid_model_or_group"
	}
	if input.CurrentChannelID < 0 || input.Result == FinalSucceeded && input.CurrentChannelID == 0 {
		return "invalid_channel"
	}
	for _, channelID := range input.UseChannelIDs {
		if channelID < 1 {
			return "invalid_channel_sequence"
		}
	}
	if input.HTTPStatus != 0 && (input.HTTPStatus < 100 || input.HTTPStatus > 599) {
		return "invalid_http_status"
	}
	if input.PromptTokens < 0 || input.CompletionTokens < 0 || input.PromptTokens > math.MaxInt64-input.CompletionTokens {
		return "invalid_token_usage"
	}
	if validateDurations(input.FirstTokenMillis, input.TotalMillis) != nil {
		return "invalid_duration"
	}
	return ""
}

func NewQuarantineFact(source Source, siteID uuid.UUID, cursor Cursor, reasonCode string) (QuarantineFact, error) {
	cursor = normalizeCursor(cursor)
	fact := QuarantineFact{
		SourceHash: stableHash("quarantine", string(source), siteID.String(), cursor.OccurredAt.Format(timeLayout), cursor.SourceID),
		Source:     source, SiteID: siteID, Cursor: cursor, ReasonCode: reasonCode,
	}
	if err := fact.Validate(); err != nil {
		return QuarantineFact{}, err
	}
	return fact, nil
}

func convertNewAPIRequest(source Source, siteID uuid.UUID, records []NewAPILogInput, resolve ChannelAttributionResolver) (RequestFact, []AttemptFact) {
	terminal := records[len(records)-1]
	sequence := make([]int64, 0)
	for _, record := range records {
		if len(record.UseChannelIDs) >= len(sequence) {
			sequence = append(sequence[:0], record.UseChannelIDs...)
		}
	}
	if len(sequence) == 0 || sequence[len(sequence)-1] != terminal.CurrentChannelID {
		sequence = append(sequence, terminal.CurrentChannelID)
	}
	requestHash := stableHash("request", string(source), siteID.String(), terminal.RequestID)
	terminalSourceHash := newAPILogSourceHash(source, siteID, terminal)
	stream := streamCompletion(terminal.IsStream, terminal.StreamCompleted)
	terminalResult := terminal.Result
	if terminalResult == FinalSucceeded && stream == StreamIncomplete {
		terminalResult = FinalFailed
		terminal.StableErrorCode = "stream_incomplete"
		terminal.ErrorText = "stream incomplete"
	}
	errorFact := ErrorFact{}
	if terminalResult == FinalFailed {
		errorFact = classifyInputError(terminal, stream)
		terminalResult = finalResultFromError(errorFact)
	}
	request := RequestFact{
		SourceHash: terminalSourceHash, RequestHash: requestHash, RuleVersion: MeasurementRuleVersion,
		Source: source, SiteID: siteID, RequestID: terminal.RequestID, TerminalCursor: terminal.Cursor,
		Model: terminal.Model, Group: terminal.Group,
		Result: terminalResult, HTTPStatus: terminal.HTTPStatus, IsStream: terminal.IsStream, StreamCompletion: stream, FinalChannelID: terminal.CurrentChannelID,
		Attribution: attributionFor(terminal.CurrentChannelID, terminal.Cursor.OccurredAt, resolve), FirstTokenMillis: copyInt64(terminal.FirstTokenMillis),
		TotalMillis: copyInt64(terminal.TotalMillis), DurationResolutionMillis: terminal.DurationResolutionMillis,
		PromptTokens: terminal.PromptTokens, CompletionTokens: terminal.CompletionTokens,
		TotalTokens: terminal.PromptTokens + terminal.CompletionTokens, AttemptCount: len(sequence), OccurredAt: records[0].Cursor.OccurredAt,
		Error: errorFact,
	}
	failedRecords := make([]NewAPILogInput, 0, len(records))
	for index, record := range records {
		if index == len(records)-1 {
			continue
		}
		if record.Result == FinalFailed {
			failedRecords = append(failedRecords, record)
		}
	}
	usedFailure := make([]bool, len(failedRecords))
	attempts := make([]AttemptFact, 0, len(sequence))
	for index, channelID := range sequence {
		ordinal := index + 1
		final := ordinal == len(sequence)
		result := AttemptFailed
		observed := terminal
		observedFailure := false
		if final && terminalResult == FinalSucceeded {
			result = AttemptSucceeded
		} else if final {
			result = attemptResultFromFinal(terminalResult)
			observedFailure = true
		} else {
			for failureIndex, record := range failedRecords {
				if !usedFailure[failureIndex] && record.CurrentChannelID == channelID {
					usedFailure[failureIndex] = true
					observed = record
					observedFailure = true
					break
				}
			}
		}
		attemptError := ErrorFact{}
		attemptStream := StreamUnknown
		attemptSourceHash := stableHash(terminalSourceHash, "attempt", strconv.Itoa(ordinal), strconv.FormatInt(channelID, 10))
		attemptTime := terminal.Cursor.OccurredAt
		attemptHTTPStatus := 0
		attemptIsStream := false
		var firstTokenMillis, totalMillis *int64
		durationResolutionMillis := int64(0)
		if result != AttemptSucceeded {
			if observedFailure {
				attemptStream = streamCompletion(observed.IsStream, observed.StreamCompleted)
				attemptError = classifyInputError(observed, attemptStream)
				result = attemptResultFromError(attemptError)
				attemptSourceHash = stableHash(newAPILogSourceHash(source, siteID, observed), "attempt", strconv.Itoa(ordinal), strconv.FormatInt(channelID, 10))
				attemptTime = observed.Cursor.OccurredAt
				attemptHTTPStatus = observed.HTTPStatus
				attemptIsStream = observed.IsStream
				firstTokenMillis = copyInt64(observed.FirstTokenMillis)
				totalMillis = copyInt64(observed.TotalMillis)
				durationResolutionMillis = observed.DurationResolutionMillis
			} else {
				attemptError = ErrorFact{Class: ErrorUnknown, Summary: "channel failed before retry", RuleVersion: ErrorClassificationRuleVersion}
			}
		} else {
			attemptStream = stream
			attemptSourceHash = stableHash(terminalSourceHash, "attempt", strconv.Itoa(ordinal), strconv.FormatInt(channelID, 10))
			attemptHTTPStatus = terminal.HTTPStatus
			attemptIsStream = terminal.IsStream
			firstTokenMillis = copyInt64(terminal.FirstTokenMillis)
			totalMillis = copyInt64(terminal.TotalMillis)
			durationResolutionMillis = terminal.DurationResolutionMillis
		}
		attribution := Attribution{Status: AttributionPending}
		if result == AttemptSucceeded || observedFailure {
			attribution = attributionFor(channelID, attemptTime, resolve)
		}
		attempts = append(attempts, AttemptFact{
			SourceHash: attemptSourceHash, RequestHash: requestHash, RuleVersion: MeasurementRuleVersion,
			Source: source, SiteID: siteID, RequestID: terminal.RequestID, Ordinal: ordinal, ChannelID: channelID, Model: observed.Model,
			Attribution: attribution, Result: result, HTTPStatus: attemptHTTPStatus, IsFinal: final, IsStream: attemptIsStream,
			ProducedVisibleOutput: firstTokenMillis != nil, StreamCompletion: attemptStream,
			FirstTokenMillis: firstTokenMillis, TotalMillis: totalMillis, DurationResolutionMillis: durationResolutionMillis,
			OccurredAt: attemptTime, Error: attemptError,
		})
	}
	request.SourceHash = newAPIRequestRevisionHash(terminalSourceHash, attempts)
	return request, attempts
}

func newAPIRequestRevisionHash(terminalSourceHash string, attempts []AttemptFact) string {
	parts := make([]string, 0, len(attempts)+2)
	parts = append(parts, "new_api_request_revision", terminalSourceHash)
	for _, attempt := range attempts {
		parts = append(parts, attempt.SourceHash)
	}
	return stableHash(parts...)
}

func classifyInputError(input NewAPILogInput, stream StreamCompletion) ErrorFact {
	classified := ClassifyError(input.StableErrorCode, input.HTTPStatus, input.ErrorText)
	if classified.Class == ErrorUnknown && stream == StreamIncomplete {
		classified.Class = ErrorStreamIncomplete
	}
	return classified
}

func finalResultFromError(fact ErrorFact) FinalResult {
	switch fact.Class {
	case ErrorCancelled:
		return FinalCancelled
	case ErrorRejected:
		return FinalRejected
	case ErrorStreamIncomplete:
		return FinalIncomplete
	default:
		return FinalFailed
	}
}

func attemptResultFromFinal(result FinalResult) AttemptResult {
	switch result {
	case FinalCancelled:
		return AttemptCancelled
	case FinalRejected:
		return AttemptRejected
	case FinalIncomplete:
		return AttemptIncomplete
	default:
		return AttemptFailed
	}
}

func attemptResultFromError(fact ErrorFact) AttemptResult {
	return attemptResultFromFinal(finalResultFromError(fact))
}

func streamCompletion(isStream bool, completed *bool) StreamCompletion {
	if !isStream || completed == nil {
		return StreamUnknown
	}
	if *completed {
		return StreamCompleted
	}
	return StreamIncomplete
}

func attributionFor(channelID int64, at time.Time, resolve ChannelAttributionResolver) Attribution {
	if resolve == nil || channelID < 1 || at.IsZero() {
		return Attribution{Status: AttributionPending}
	}
	value := resolve(channelID, at.UTC())
	if value.Status != AttributionMapped || value.RelationID == uuid.Nil || value.SupplierID == uuid.Nil {
		return Attribution{Status: AttributionPending}
	}
	return value
}

func newAPILogSourceHash(source Source, siteID uuid.UUID, input NewAPILogInput) string {
	channels := make([]string, 0, len(input.UseChannelIDs))
	for _, channelID := range input.UseChannelIDs {
		channels = append(channels, strconv.FormatInt(channelID, 10))
	}
	return stableHash(
		"new_api_log", string(source), siteID.String(), input.Cursor.OccurredAt.Format(timeLayout), input.Cursor.SourceID,
		input.RequestID, string(input.Result), input.Model, input.Group, strconv.FormatInt(input.CurrentChannelID, 10),
		strings.Join(channels, ","), input.StableErrorCode, strconv.Itoa(input.HTTPStatus), strconv.FormatBool(input.IsStream),
		boolPointerValue(input.StreamCompleted), int64PointerValue(input.FirstTokenMillis), int64PointerValue(input.TotalMillis),
		strconv.FormatInt(input.DurationResolutionMillis, 10), strconv.FormatInt(input.PromptTokens, 10), strconv.FormatInt(input.CompletionTokens, 10),
	)
}

func boolPointerValue(value *bool) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatBool(*value)
}

func int64PointerValue(value *int64) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatInt(*value, 10)
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func stableHash(parts ...string) string {
	digest := sha256.New()
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = digest.Write(size[:])
		_, _ = digest.Write([]byte(part))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func normalizeCursor(cursor Cursor) Cursor {
	if !cursor.OccurredAt.IsZero() {
		cursor.OccurredAt = cursor.OccurredAt.UTC()
	}
	return cursor
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
