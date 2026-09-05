package measurement_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

func TestConvertNewAPILogsBuildsRetryAttemptsAndKeepsUnknownChannels(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	relationID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	supplierID := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	start := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	complete := true
	total := int64(900)
	first := int64(240)
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{
		{
			Cursor: measurement.Cursor{OccurredAt: start, SourceID: "101"}, RequestID: "request-a", Result: measurement.FinalFailed,
			Model: "model-a", Group: "mrab", CurrentChannelID: 41, UseChannelIDs: []int64{41}, StableErrorCode: "upstream_timeout", HTTPStatus: 500, ErrorText: "timeout",
		},
		{
			Cursor: measurement.Cursor{OccurredAt: start.Add(time.Second), SourceID: "102"}, RequestID: "request-a", Result: measurement.FinalSucceeded,
			Model: "model-a", Group: "mrab", CurrentChannelID: 42, UseChannelIDs: []int64{41}, HTTPStatus: 200, IsStream: true, StreamCompleted: &complete,
			FirstTokenMillis: &first, TotalMillis: &total, DurationResolutionMillis: measurement.DurationResolutionMillisecond,
			PromptTokens: 12, CompletionTokens: 3,
		},
	}, map[int64]measurement.ChannelAttribution{41: {RelationID: relationID, SupplierID: supplierID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Requests) != 1 || len(batch.Attempts) != 2 {
		t.Fatalf("facts: requests=%d attempts=%d", len(batch.Requests), len(batch.Attempts))
	}
	request := batch.Requests[0]
	if request.Result != measurement.FinalSucceeded || request.HTTPStatus != 200 || !request.IsStream || request.FinalChannelID != 42 || request.AttemptCount != 2 || request.TotalTokens != 15 || request.StreamCompletion != measurement.StreamCompleted {
		t.Fatalf("request fact = %#v", request)
	}
	if batch.Attempts[0].ChannelID != 41 || batch.Attempts[0].Model != "model-a" || batch.Attempts[0].Result != measurement.AttemptFailed || batch.Attempts[0].HTTPStatus != 500 || batch.Attempts[0].IsFinal || batch.Attempts[0].ProducedVisibleOutput || batch.Attempts[0].Error.Class != measurement.ErrorTimeout || batch.Attempts[0].Attribution.Status != measurement.AttributionMapped {
		t.Fatalf("first attempt = %#v", batch.Attempts[0])
	}
	if batch.Attempts[1].ChannelID != 42 || batch.Attempts[1].Model != "model-a" || batch.Attempts[1].Result != measurement.AttemptSucceeded || batch.Attempts[1].HTTPStatus != 200 || !batch.Attempts[1].IsFinal || !batch.Attempts[1].IsStream || !batch.Attempts[1].ProducedVisibleOutput || batch.Attempts[1].Attribution.Status != measurement.AttributionPending {
		t.Fatalf("final attempt = %#v", batch.Attempts[1])
	}
	if request.Attribution.Status != measurement.AttributionPending || request.Attribution.RelationID != uuid.Nil || request.Attribution.SupplierID != uuid.Nil {
		t.Fatalf("unknown final channel was not retained as pending: %#v", request.Attribution)
	}
}

func TestConvertNewAPILogsHashAndFactsIgnoreSensitiveFields(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000002")
	base := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC), SourceID: "201"},
		RequestID: "request-b", Result: measurement.FinalFailed, Model: "model-b", Group: "dedicated", CurrentChannelID: 55,
		HTTPStatus: 503, ErrorText: "service unavailable", Username: "first-user", IP: "192.0.2.1", RequestBody: "private prompt", ResponseBody: "private output", APIKey: "sk-first-secret",
	}
	changed := base
	changed.Username = "second-user"
	changed.IP = "198.51.100.2"
	changed.RequestBody = "another prompt"
	changed.ResponseBody = "another output"
	changed.APIKey = "sk-second-secret"
	first, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{base}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{changed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("sensitive source fields changed normalized facts or hashes")
	}
}

func TestNewAPILogContentChangesCreateRevisionWithoutChangingCursor(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000012")
	cursor := measurement.Cursor{OccurredAt: time.Date(2026, 9, 5, 13, 30, 0, 0, time.UTC), SourceID: "2:00000000000000000123"}
	firstDuration, correctedDuration := int64(1000), int64(2000)
	base := measurement.NewAPILogInput{
		Cursor: cursor, RequestID: "request-correction", Result: measurement.FinalSucceeded,
		Model: "model", Group: "default", CurrentChannelID: 12, TotalMillis: &firstDuration,
		DurationResolutionMillis: measurement.DurationResolutionSecond,
	}
	corrected := base
	corrected.TotalMillis = &correctedDuration
	first, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{base}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{corrected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Requests[0].TerminalCursor != second.Requests[0].TerminalCursor || first.Requests[0].SourceHash == second.Requests[0].SourceHash {
		t.Fatalf("content correction identity = first %#v second %#v", first.Requests[0], second.Requests[0])
	}
}

func TestConvertNewAPILogsGroupsRequestsAndAdvancesStableCursor(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000003")
	start := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)
	from := measurement.Cursor{OccurredAt: start, SourceID: "300"}
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceSiteProbe, siteID, from, []measurement.NewAPILogInput{
		{Cursor: measurement.Cursor{OccurredAt: start.Add(2 * time.Second), SourceID: "302"}, RequestID: "request-d", Result: measurement.FinalFailed, Model: "model", CurrentChannelID: 62, HTTPStatus: 429},
		{Cursor: measurement.Cursor{OccurredAt: start.Add(time.Second), SourceID: "301"}, RequestID: "request-c", Result: measurement.FinalSucceeded, Model: "model", CurrentChannelID: 61},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Requests) != 2 || batch.Requests[0].RequestID != "request-c" || batch.Requests[1].RequestID != "request-d" {
		t.Fatalf("request ordering = %#v", batch.Requests)
	}
	if batch.Next.SourceID != "302" || batch.Requests[1].Error.Class != measurement.ErrorRateLimited {
		t.Fatalf("batch result = %#v", batch)
	}
}

func TestConvertNewAPILogsKeepsStableRequestIdentityAcrossTerminalRevisions(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000004")
	start := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	failure := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: start, SourceID: "0:error"},
		RequestID: "request-revised", Result: measurement.FinalFailed, Model: "model", CurrentChannelID: 71, HTTPStatus: 503,
	}
	success := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: start, SourceID: "1:consume"},
		RequestID: "request-revised", Result: measurement.FinalSucceeded, Model: "model", CurrentChannelID: 72,
		UseChannelIDs: []int64{71}, HTTPStatus: 200,
	}
	first, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{failure}, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{failure, success}, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldRequest, currentRequest := first.Requests[0], updated.Requests[0]
	if oldRequest.RequestHash != currentRequest.RequestHash || oldRequest.SourceHash == currentRequest.SourceHash {
		t.Fatalf("request identity and revision evidence were not separated: old=%#v current=%#v", oldRequest, currentRequest)
	}
	if oldRequest.TerminalCursor.Compare(failure.Cursor) != 0 || currentRequest.TerminalCursor.Compare(success.Cursor) != 0 {
		t.Fatalf("terminal revision cursors were not preserved: old=%#v current=%#v", oldRequest.TerminalCursor, currentRequest.TerminalCursor)
	}
	if oldRequest.Result != measurement.FinalFailed || currentRequest.Result != measurement.FinalSucceeded || len(first.Attempts) != 1 || len(updated.Attempts) != 2 {
		t.Fatalf("terminal revision did not replace the complete request view: first=%#v updated=%#v", first, updated)
	}
}

func TestConvertNewAPILogsRevisionHashIncludesLateAttemptEvidence(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000009")
	started := time.Date(2026, 9, 5, 15, 15, 0, 0, time.UTC)
	failure := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: started, SourceID: "0:901"},
		RequestID: "request-late-attempt", Result: measurement.FinalFailed, Model: "model", Group: "default",
		CurrentChannelID: 91, HTTPStatus: 504,
	}
	terminal := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: started.Add(time.Second), SourceID: "1:902"},
		RequestID: "request-late-attempt", Result: measurement.FinalSucceeded, Model: "model", Group: "default",
		CurrentChannelID: 92, UseChannelIDs: []int64{91}, HTTPStatus: 200,
	}
	partial, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{terminal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{failure, terminal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{failure, terminal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Requests[0].TerminalCursor.Compare(complete.Requests[0].TerminalCursor) != 0 {
		t.Fatal("test did not keep the terminal cursor stable")
	}
	if partial.Requests[0].RequestHash != complete.Requests[0].RequestHash || partial.Requests[0].SourceHash == complete.Requests[0].SourceHash {
		t.Fatalf("late attempt did not create a new revision identity: partial=%#v complete=%#v", partial.Requests[0], complete.Requests[0])
	}
	if complete.Requests[0].SourceHash != replayed.Requests[0].SourceHash {
		t.Fatal("identical replay changed the revision identity")
	}
}

func TestConvertNewAPILogsQuarantinesLocatableInvalidRecord(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000007")
	started := time.Date(2026, 9, 5, 15, 30, 0, 0, time.UTC)
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{
		{Cursor: measurement.Cursor{OccurredAt: started, SourceID: "701"}, RequestID: "poison", Result: measurement.FinalSucceeded, Model: "", CurrentChannelID: 71},
		{Cursor: measurement.Cursor{OccurredAt: started.Add(time.Second), SourceID: "702"}, RequestID: "valid", Result: measurement.FinalSucceeded, Model: "model", CurrentChannelID: 72},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Quarantines) != 1 || batch.Quarantines[0].ReasonCode != "invalid_model_or_group" || batch.Quarantines[0].Cursor.SourceID != "701" {
		t.Fatalf("invalid record was not quarantined: %#v", batch.Quarantines)
	}
	if len(batch.Requests) != 1 || batch.Requests[0].RequestID != "valid" || batch.Next.SourceID != "702" {
		t.Fatalf("valid record after quarantine was lost: %#v", batch)
	}
}

func TestConvertNewAPILogsPreservesThreeStreamStates(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000005")
	start := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	completed, incomplete := true, false
	inputs := []measurement.NewAPILogInput{
		{Cursor: measurement.Cursor{OccurredAt: start, SourceID: "501"}, RequestID: "unknown-stream", Result: measurement.FinalSucceeded, Model: "model", CurrentChannelID: 81},
		{Cursor: measurement.Cursor{OccurredAt: start.Add(time.Second), SourceID: "502"}, RequestID: "complete-stream", Result: measurement.FinalSucceeded, Model: "model", CurrentChannelID: 82, IsStream: true, StreamCompleted: &completed},
		{Cursor: measurement.Cursor{OccurredAt: start.Add(2 * time.Second), SourceID: "503"}, RequestID: "incomplete-stream", Result: measurement.FinalFailed, Model: "model", CurrentChannelID: 83, IsStream: true, StreamCompleted: &incomplete},
	}
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []measurement.StreamCompletion{measurement.StreamUnknown, measurement.StreamCompleted, measurement.StreamIncomplete}
	for index, request := range batch.Requests {
		if request.StreamCompletion != want[index] {
			t.Fatalf("stream state %d = %q", index, request.StreamCompletion)
		}
	}
	if batch.Requests[2].Error.Class != measurement.ErrorStreamIncomplete {
		t.Fatalf("incomplete stream classification = %#v", batch.Requests[2].Error)
	}
}

func TestConvertNewAPILogsRejectsSucceededTerminalWithIncompleteStream(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000008")
	incomplete := false
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{{
		Cursor:    measurement.Cursor{OccurredAt: time.Date(2026, 9, 5, 16, 30, 0, 0, time.UTC), SourceID: "801"},
		RequestID: "consume-incomplete", Result: measurement.FinalSucceeded, Model: "model", Group: "default",
		CurrentChannelID: 84, IsStream: true, StreamCompleted: &incomplete,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Requests[0].Result != measurement.FinalIncomplete || batch.Requests[0].Error.Class != measurement.ErrorStreamIncomplete {
		t.Fatalf("request remained successful: %#v", batch.Requests[0])
	}
	if batch.Attempts[0].Result != measurement.AttemptIncomplete || batch.Attempts[0].Error.Class != measurement.ErrorStreamIncomplete {
		t.Fatalf("final attempt remained successful: %#v", batch.Attempts[0])
	}
}

func TestConvertNewAPILogsPreservesNonSupplierTerminalOutcomes(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000009")
	started := time.Date(2026, 9, 5, 16, 40, 0, 0, time.UTC)
	tests := []struct {
		name        string
		code        string
		request     measurement.FinalResult
		attempt     measurement.AttemptResult
		responsible measurement.ErrorResponsibility
	}{
		{name: "content rejected", code: "content_filter", request: measurement.FinalRejected, attempt: measurement.AttemptRejected, responsible: measurement.ResponsibilityUser},
		{name: "user cancelled", code: "client_cancelled", request: measurement.FinalCancelled, attempt: measurement.AttemptCancelled, responsible: measurement.ResponsibilityUser},
	}
	for index, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{{
				Cursor:    measurement.Cursor{OccurredAt: started.Add(time.Duration(index) * time.Second), SourceID: test.name},
				RequestID: test.name, Result: measurement.FinalFailed, Model: "model", Group: "default",
				CurrentChannelID: 90 + int64(index), StableErrorCode: test.code, HTTPStatus: 400,
			}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if batch.Requests[0].Result != test.request || batch.Attempts[0].Result != test.attempt || batch.Requests[0].Error.ResolvedResponsibility() != test.responsible {
				t.Fatalf("terminal outcome = request %#v attempt %#v", batch.Requests[0], batch.Attempts[0])
			}
		})
	}
}

func TestRepeatedChannelRetriesHaveDistinctFactHashes(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000006")
	start := time.Date(2026, 9, 5, 17, 0, 0, 0, time.UTC)
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{{
		Cursor: measurement.Cursor{OccurredAt: start, SourceID: "601"}, RequestID: "same-channel-retry", Result: measurement.FinalFailed,
		Model: "model", CurrentChannelID: 91, UseChannelIDs: []int64{91, 91}, HTTPStatus: 503,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Attempts) != 2 || batch.Attempts[0].SourceHash == batch.Attempts[1].SourceHash {
		t.Fatalf("repeated attempts lost identity: %#v", batch.Attempts)
	}
	if batch.Attempts[0].Error.Class != measurement.ErrorUnknown || batch.Attempts[0].IsFinal || !batch.Attempts[1].IsFinal {
		t.Fatalf("terminal failure was assigned to the wrong retry: %#v", batch.Attempts)
	}
}
