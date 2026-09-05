package measurement_test

import (
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

func TestMeasurementValidationQuarantinesLocatableInconsistentFacts(t *testing.T) {
	t.Parallel()
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000004")
	now := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	complete := true
	first := int64(200)
	total := int64(100)
	batch, err := measurement.ConvertNewAPILogs(measurement.SourceRealTraffic, siteID, measurement.Cursor{}, []measurement.NewAPILogInput{{
		Cursor: measurement.Cursor{OccurredAt: now, SourceID: "401"}, RequestID: "request-e", Result: measurement.FinalSucceeded,
		Model: "model", CurrentChannelID: 71, IsStream: true, StreamCompleted: &complete, FirstTokenMillis: &first, TotalMillis: &total,
		DurationResolutionMillis: measurement.DurationResolutionMillisecond,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Requests) != 0 || len(batch.Attempts) != 0 || len(batch.Quarantines) != 1 || batch.Quarantines[0].ReasonCode != "invalid_duration" {
		t.Fatalf("invalid timing was not quarantined: %#v", batch)
	}

	_, err = measurement.ConvertNewAPILogs(measurement.Source("unknown"), siteID, measurement.Cursor{}, nil, nil)
	if err == nil {
		t.Fatal("unknown measurement source was accepted")
	}
}

func TestMeasurementSourcesAreFixed(t *testing.T) {
	t.Parallel()
	for _, source := range []measurement.Source{measurement.SourceRealTraffic, measurement.SourceDirectProbe, measurement.SourceSiteProbe} {
		if !source.Valid() {
			t.Fatalf("declared source %q is invalid", source)
		}
	}
	if measurement.Source("other").Valid() {
		t.Fatal("unknown source was accepted")
	}
}
