package scoring_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	applicationscoring "github.com/evepupil/ManyRouter/internal/application/scoring"
)

func TestListInsightsDefaultsPaginationAndTrimsModel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 21, 0, 0, 0, time.UTC)
	repository := newFakeRepository(now)
	service := newService(t, repository, now)

	if _, err := service.ListInsights(context.Background(), applicationscoring.InsightFilter{Model: "  model-a  "}); err != nil {
		t.Fatal(err)
	}
	if len(repository.listFilters) != 1 {
		t.Fatalf("repository called %d times", len(repository.listFilters))
	}
	filter := repository.listFilters[0]
	if filter.Limit != 20 || filter.Offset != 0 || filter.Model != "model-a" {
		t.Fatalf("normalized filter = %#v", filter)
	}
}

func TestListInsightsAcceptsPaginationBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 21, 0, 0, 0, time.UTC)
	for _, limit := range []int{1, 100} {
		limit := limit
		t.Run("limit-"+strconv.Itoa(limit), func(t *testing.T) {
			t.Parallel()
			repository := newFakeRepository(now)
			service := newService(t, repository, now)
			if _, err := service.ListInsights(context.Background(), applicationscoring.InsightFilter{Limit: limit, Offset: 10}); err != nil {
				t.Fatal(err)
			}
			if len(repository.listFilters) != 1 || repository.listFilters[0].Limit != limit || repository.listFilters[0].Offset != 10 {
				t.Fatalf("pagination was changed: %#v", repository.listFilters)
			}
		})
	}
}

func TestListInsightsRejectsInvalidPaginationBeforeRepositoryCall(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 21, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		filter applicationscoring.InsightFilter
	}{
		{name: "negative limit", filter: applicationscoring.InsightFilter{Limit: -1}},
		{name: "limit above maximum", filter: applicationscoring.InsightFilter{Limit: 101}},
		{name: "negative offset", filter: applicationscoring.InsightFilter{Limit: 20, Offset: -1}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newFakeRepository(now)
			service := newService(t, repository, now)
			if _, err := service.ListInsights(context.Background(), test.filter); err == nil {
				t.Fatalf("invalid pagination was accepted: %#v", test.filter)
			}
			if len(repository.listFilters) != 0 {
				t.Fatalf("repository received invalid pagination: %#v", repository.listFilters)
			}
		})
	}
}
