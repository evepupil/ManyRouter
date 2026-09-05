package collection

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

func newAPIMeasurementInputs(siteID uuid.UUID, logs []RemoteLog) ([]measurement.NewAPILogInput, []measurement.QuarantineFact, time.Time, error) {
	inputs := make([]measurement.NewAPILogInput, 0, len(logs))
	quarantines := make([]measurement.QuarantineFact, 0)
	var latest time.Time
	for _, log := range logs {
		cursor, err := newAPILogCursor(log)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		if cursor.OccurredAt.After(latest) {
			latest = cursor.OccurredAt
		}
		input, err := newAPIMeasurementInputWithCursor(log, cursor)
		if err != nil {
			var invalid *quarantinableSourceLogError
			if !errors.As(err, &invalid) {
				return nil, nil, time.Time{}, err
			}
			quarantine, quarantineErr := measurement.NewQuarantineFact(measurement.SourceRealTraffic, siteID, cursor, invalid.reasonCode)
			if quarantineErr != nil {
				return nil, nil, time.Time{}, quarantineErr
			}
			quarantines = append(quarantines, quarantine)
			continue
		}
		inputs = append(inputs, input)
	}
	return inputs, quarantines, latest, nil
}

func newAPIMeasurementInput(log RemoteLog) (measurement.NewAPILogInput, error) {
	cursor, err := newAPILogCursor(log)
	if err != nil {
		return measurement.NewAPILogInput{}, err
	}
	return newAPIMeasurementInputWithCursor(log, cursor)
}

func newAPILogCursor(log RemoteLog) (measurement.Cursor, error) {
	if log.ID < 0 || log.CreatedAt <= 0 {
		return measurement.Cursor{}, errors.New("new API log identity cannot form a stable cursor")
	}
	return measurement.Cursor{OccurredAt: time.Unix(log.CreatedAt, 0).UTC(), SourceID: newAPISourceID(log)}, nil
}

type quarantinableSourceLogError struct {
	reasonCode string
}

func (err *quarantinableSourceLogError) Error() string {
	return "new API log fields are invalid"
}

func newAPIMeasurementInputWithCursor(log RemoteLog, cursor measurement.Cursor) (measurement.NewAPILogInput, error) {
	if log.DurationSeconds < 0 || log.DurationSeconds > math.MaxInt64/1000 {
		return measurement.NewAPILogInput{}, &quarantinableSourceLogError{reasonCode: "invalid_duration"}
	}
	result := measurement.FinalSucceeded
	if log.Type == newAPIErrorLogType {
		result = measurement.FinalFailed
	} else if log.Type != newAPIConsumeLogType {
		return measurement.NewAPILogInput{}, &quarantinableSourceLogError{reasonCode: "unsupported_log_type"}
	}
	metadata := parseNewAPILogMetadata(log.Other)
	if result == measurement.FinalSucceeded && metadata.streamCompleted != nil && !*metadata.streamCompleted {
		result = measurement.FinalFailed
		if metadata.errorCode == "" {
			metadata.errorCode = "stream_incomplete"
		}
		if strings.TrimSpace(log.ErrorText) == "" {
			log.ErrorText = "stream incomplete"
		}
	}
	totalMillis := log.DurationSeconds * 1000
	return measurement.NewAPILogInput{
		Cursor:    cursor,
		RequestID: log.RequestID, Result: result, Model: log.Model, Group: log.Group,
		CurrentChannelID: log.ChannelID, UseChannelIDs: metadata.useChannelIDs,
		StableErrorCode: metadata.errorCode, HTTPStatus: metadata.httpStatus,
		ErrorText: log.ErrorText, IsStream: log.Stream, StreamCompleted: metadata.streamCompleted,
		FirstTokenMillis: metadata.firstTokenMillis, TotalMillis: &totalMillis,
		DurationResolutionMillis: measurement.DurationResolutionSecond,
		PromptTokens:             log.InputTokens, CompletionTokens: log.OutputTokens,
	}, nil
}

type newAPILogMetadata struct {
	useChannelIDs    []int64
	errorCode        string
	httpStatus       int
	firstTokenMillis *int64
	streamCompleted  *bool
}

func parseNewAPILogMetadata(raw string) newAPILogMetadata {
	if strings.TrimSpace(raw) == "" {
		return newAPILogMetadata{}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var data map[string]any
	if decoder.Decode(&data) != nil {
		return newAPILogMetadata{}
	}
	metadata := newAPILogMetadata{}
	if value, ok := numberAt(data, "frt"); ok && value >= 0 {
		milliseconds := int64(math.Round(value))
		metadata.firstTokenMillis = &milliseconds
	}
	metadata.errorCode, _ = stringAt(data, "error_code")
	metadata.httpStatus = intAt(data, "upstream_status_code")
	if metadata.httpStatus == 0 {
		metadata.httpStatus = intAt(data, "status_code")
	}
	if admin, ok := objectAt(data, "admin_info"); ok {
		metadata.useChannelIDs = int64SliceAt(admin, "use_channel")
		if metadata.errorCode == "" {
			metadata.errorCode, _ = stringAt(admin, "error_code")
		}
		if metadata.httpStatus == 0 {
			metadata.httpStatus = intAt(admin, "upstream_status_code")
		}
	}
	if stream, ok := objectAt(data, "stream_status"); ok {
		status, _ := stringAt(stream, "status")
		endReason, _ := stringAt(stream, "end_reason")
		if completed, known := streamCompletionStatus(status, endReason); known {
			metadata.streamCompleted = &completed
		}
	}
	return metadata
}

func streamCompletionStatus(status, endReason string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "completed", "done":
		return true, true
	case "error", "failed", "incomplete":
		return false, true
	}
	switch strings.ToLower(strings.TrimSpace(endReason)) {
	case "done", "eof", "handler_stop":
		return true, true
	case "timeout", "client_gone", "scanner_error", "panic", "ping_fail":
		return false, true
	default:
		return false, false
	}
}

func newAPISourceID(log RemoteLog) string {
	return fmt.Sprintf("%d:%020d", newAPILogOrder(log.Type), log.ID)
}

func objectAt(values map[string]any, key string) (map[string]any, bool) {
	value, ok := values[key].(map[string]any)
	return value, ok
}

func stringAt(values map[string]any, key string) (string, bool) {
	value, ok := values[key].(string)
	return strings.TrimSpace(value), ok
}

func numberAt(values map[string]any, key string) (float64, bool) {
	switch value := values[key].(type) {
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	case float64:
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	default:
		return 0, false
	}
}

func intAt(values map[string]any, key string) int {
	value, ok := numberAt(values, key)
	if !ok || value < 100 || value > 599 || math.Trunc(value) != value {
		return 0
	}
	return int(value)
}

func int64SliceAt(values map[string]any, key string) []int64 {
	items, ok := values[key].([]any)
	if !ok {
		return nil
	}
	result := make([]int64, 0, len(items))
	for _, item := range items {
		var value int64
		switch current := item.(type) {
		case json.Number:
			parsed, err := current.Int64()
			if err != nil {
				continue
			}
			value = parsed
		case string:
			parsed, err := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
			if err != nil {
				continue
			}
			value = parsed
		default:
			continue
		}
		if value > 0 {
			result = append(result, value)
		}
	}
	return result
}
