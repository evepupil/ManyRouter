package collection

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

func TestNewAPIMeasurementInputParsesTimingStreamAndChannels(t *testing.T) {
	t.Parallel()
	input, err := newAPIMeasurementInput(RemoteLog{
		ID: 17, CreatedAt: 1_788_600_000, Type: newAPIErrorLogType, Model: "model-a",
		DurationSeconds: 2, Stream: true, ChannelID: 13, Group: "mrab", RequestID: "request-a",
		ErrorText: `api_key=sk-sensitive rate limited`,
		Other:     `{"frt":125.6,"status_code":429,"admin_info":{"use_channel":["11",12,"13"],"error_code":"rate_limited"},"stream_status":{"status":"error","end_reason":"timeout"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Result != measurement.FinalFailed || input.StableErrorCode != "rate_limited" || input.HTTPStatus != 429 {
		t.Fatalf("error metadata = %#v", input)
	}
	if input.FirstTokenMillis == nil || *input.FirstTokenMillis != 126 || input.TotalMillis == nil || *input.TotalMillis != 2000 {
		t.Fatalf("timing metadata = %#v", input)
	}
	if !input.IsStream || input.StreamCompleted == nil || *input.StreamCompleted {
		t.Fatalf("stream metadata = %#v", input)
	}
	if !slices.Equal(input.UseChannelIDs, []int64{11, 12, 13}) {
		t.Fatalf("channel sequence = %v", input.UseChannelIDs)
	}
	if input.Cursor.SourceID != "0:00000000000000000017" || input.Cursor.OccurredAt.Location() != time.UTC {
		t.Fatalf("source cursor = %#v", input.Cursor)
	}
}

func TestNewAPIConsumeLogWithIncompleteStreamIsFailed(t *testing.T) {
	t.Parallel()
	input, err := newAPIMeasurementInput(RemoteLog{
		ID: 18, CreatedAt: 1_788_600_001, Type: newAPIConsumeLogType, Model: "model-a",
		DurationSeconds: 2, Stream: true, ChannelID: 14, Group: "mrab", RequestID: "request-incomplete",
		Other: `{"stream_status":{"status":"incomplete","end_reason":"timeout"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Result != measurement.FinalFailed || input.StableErrorCode != "stream_incomplete" {
		t.Fatalf("incomplete consume log = %#v", input)
	}
	if input.StreamCompleted == nil || *input.StreamCompleted {
		t.Fatalf("incomplete stream state = %#v", input.StreamCompleted)
	}
}

func TestNewAPILogsMergeErrorsIntoSuccessfulRetryChain(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	logs := []RemoteLog{
		{ID: 21, CreatedAt: started.Unix(), Type: newAPIErrorLogType, Model: "model-a", DurationSeconds: 1, ChannelID: 31, Group: "mrab", RequestID: "request-retry", ErrorText: "upstream timeout", Other: `{"status_code":504,"admin_info":{"use_channel":["31"]}}`},
		{ID: 22, CreatedAt: started.Unix(), Type: newAPIConsumeLogType, Model: "model-a", InputTokens: 8, OutputTokens: 2, DurationSeconds: 2, Stream: true, ChannelID: 32, Group: "mrab", RequestID: "request-retry", Other: `{"frt":240,"admin_info":{"use_channel":["31","32"]},"stream_status":{"status":"ok","end_reason":"eof"}}`},
	}
	inputs, quarantines, latest, err := newAPIMeasurementInputs(uuid.New(), logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantines) != 0 {
		t.Fatalf("valid logs were quarantined: %#v", quarantines)
	}
	relationA := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	supplierA := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	relationB := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	supplierB := uuid.MustParse("30000000-0000-0000-0000-000000000002")
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	bindings := []ChannelBinding{
		{ChannelID: 31, RelationID: relationA, SupplierID: supplierA, ValidFrom: started.Add(-time.Hour)},
		{ChannelID: 32, RelationID: relationB, SupplierID: supplierB, ValidFrom: started.Add(-time.Hour)},
	}
	batch, err := convertByRequest(siteID, measurement.Cursor{}, inputs, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if latest != started || len(batch.Requests) != 1 || len(batch.Attempts) != 2 {
		t.Fatalf("converted batch = %#v", batch)
	}
	request := batch.Requests[0]
	if request.Result != measurement.FinalSucceeded || request.FinalChannelID != 32 || request.AttemptCount != 2 || request.TotalTokens != 10 {
		t.Fatalf("request fact = %#v", request)
	}
	if batch.Attempts[0].Result != measurement.AttemptFailed || batch.Attempts[0].Error.Class != measurement.ErrorTimeout || batch.Attempts[0].ChannelID != 31 || batch.Attempts[0].IsFinal {
		t.Fatalf("failed retry = %#v", batch.Attempts[0])
	}
	if batch.Attempts[1].Result != measurement.AttemptSucceeded || batch.Attempts[1].ChannelID != 32 || !batch.Attempts[1].IsFinal || !batch.Attempts[1].ProducedVisibleOutput {
		t.Fatalf("successful retry = %#v", batch.Attempts[1])
	}
}

func TestRetryAttemptsUseChannelBindingAtTheirOwnTimes(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 9, 5, 12, 20, 0, 0, time.UTC)
	oldRelation, oldSupplier := uuid.New(), uuid.New()
	newRelation, newSupplier := uuid.New(), uuid.New()
	boundary := started.Add(time.Second)
	bindings := []ChannelBinding{
		{ChannelID: 31, RelationID: oldRelation, SupplierID: oldSupplier, ValidFrom: started.Add(-time.Hour), ValidTo: &boundary},
		{ChannelID: 31, RelationID: newRelation, SupplierID: newSupplier, ValidFrom: boundary},
	}
	batch, err := convertByRequest(uuid.New(), measurement.Cursor{}, []measurement.NewAPILogInput{
		{Cursor: measurement.Cursor{OccurredAt: started, SourceID: "retry-before-rebind"}, RequestID: "request-rebind", Result: measurement.FinalFailed, Model: "model", Group: "group", CurrentChannelID: 31, UseChannelIDs: []int64{31}, HTTPStatus: 504},
		{Cursor: measurement.Cursor{OccurredAt: boundary, SourceID: "success-after-rebind"}, RequestID: "request-rebind", Result: measurement.FinalSucceeded, Model: "model", Group: "group", CurrentChannelID: 31, UseChannelIDs: []int64{31, 31}, HTTPStatus: 200},
	}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Attempts) != 2 {
		t.Fatalf("attempts = %d", len(batch.Attempts))
	}
	if batch.Attempts[0].Attribution.SupplierID != oldSupplier || batch.Attempts[0].Attribution.RelationID != oldRelation {
		t.Fatalf("early attempt used the later binding: %#v", batch.Attempts[0].Attribution)
	}
	if batch.Attempts[1].Attribution.SupplierID != newSupplier || batch.Attempts[1].Attribution.RelationID != newRelation || batch.Requests[0].Attribution.SupplierID != newSupplier {
		t.Fatalf("final attempt did not use the current binding: request=%#v attempt=%#v", batch.Requests[0].Attribution, batch.Attempts[1].Attribution)
	}
}

func TestNewAPIErrorTextIsSanitizedBeforeBecomingFact(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000002")
	logs := []RemoteLog{{
		ID: 41, CreatedAt: time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC).Unix(), Type: newAPIErrorLogType,
		Model: "model", ChannelID: 51, RequestID: "request-secret", ErrorText: "Authorization=private-token Bearer second-token api_key=sk-third-secret timeout",
	}}
	inputs, quarantines, _, err := newAPIMeasurementInputs(siteID, logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantines) != 0 {
		t.Fatalf("valid error log was quarantined: %#v", quarantines)
	}
	batch, err := convertByRequest(siteID, measurement.Cursor{}, inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary := batch.Requests[0].Error.Summary
	for _, secret := range []string{"private-token", "second-token", "sk-third-secret"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("error summary retained %q: %s", secret, summary)
		}
	}
	if batch.Requests[0].Error.Class != measurement.ErrorTimeout {
		t.Fatalf("sanitized fallback classification = %#v", batch.Requests[0].Error)
	}
}

func TestNewAPIMeasurementInputsQuarantinesInvalidFieldsAndKeepsLaterLog(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000003")
	started := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)
	logs := []RemoteLog{
		{ID: 51, CreatedAt: started.Unix(), Type: newAPIConsumeLogType, Model: "model", DurationSeconds: -1, ChannelID: 61, RequestID: "poison-request", Other: `{"private":"must-not-persist"}`},
		{ID: 52, CreatedAt: started.Add(time.Second).Unix(), Type: newAPIConsumeLogType, Model: "model", DurationSeconds: 2, ChannelID: 62, RequestID: "valid-request"},
	}
	inputs, quarantines, latest, err := newAPIMeasurementInputs(siteID, logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].RequestID != "valid-request" || latest != started.Add(time.Second) {
		t.Fatalf("valid log after poison was lost: inputs=%#v latest=%s", inputs, latest)
	}
	if len(quarantines) != 1 || quarantines[0].ReasonCode != "invalid_duration" || quarantines[0].Cursor.OccurredAt != started || len(quarantines[0].SourceHash) != 64 {
		t.Fatalf("invalid log was not safely quarantined: %#v", quarantines)
	}
}

func TestNewAPIMeasurementInputsRejectsLogWithoutStableCursor(t *testing.T) {
	t.Parallel()
	_, _, _, err := newAPIMeasurementInputs(uuid.New(), []RemoteLog{{
		ID: 53, CreatedAt: 0, Type: newAPIConsumeLogType, Model: "model", ChannelID: 63, RequestID: "missing-time",
	}})
	if err == nil {
		t.Fatal("log without a stable timestamp was quarantined instead of rejecting the source contract")
	}
}
