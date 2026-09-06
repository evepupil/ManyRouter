package scoring_test

import (
	"context"
	"errors"
	"testing"
	"time"

	applicationscoring "github.com/evepupil/ManyRouter/internal/application/scoring"
	"github.com/google/uuid"
)

type recordedScoreRun struct {
	run       applicationscoring.ScoreRun
	completed int
	succeeded bool
	summary   string
}

type scoreRunRepository struct {
	*fakeRepository
	runs []recordedScoreRun
}

func (repository *scoreRunRepository) BeginScoreRun(_ context.Context, run applicationscoring.ScoreRun) (bool, error) {
	repository.runs = append(repository.runs, recordedScoreRun{run: run})
	return true, nil
}

func (repository *scoreRunRepository) FinishScoreRun(_ context.Context, id uuid.UUID, completed int, succeeded bool, summary string, _ time.Time) error {
	for index := range repository.runs {
		if repository.runs[index].run.ID == id {
			repository.runs[index].completed = completed
			repository.runs[index].succeeded = succeeded
			repository.runs[index].summary = summary
			return nil
		}
	}
	return errors.New("score run was not started")
}

func TestRefreshRecordsCompleteScoreRunPerSite(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	first := scoringTarget(1)
	second := scoringTarget(2)
	second.SiteID = first.SiteID
	base := newFakeRepository(now, first, second)
	repository := &scoreRunRepository{fakeRepository: base}
	service, err := applicationscoring.NewService(repository, func() time.Time { return now }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.runs) != 1 || repository.runs[0].run.ExpectedTargets != 2 ||
		repository.runs[0].completed != 2 || !repository.runs[0].succeeded {
		t.Fatalf("unexpected score run: %#v", repository.runs)
	}
	for _, snapshot := range repository.snapshots {
		if snapshot.ScoreRunID == nil || *snapshot.ScoreRunID != repository.runs[0].run.ID {
			t.Fatalf("snapshot missing score run: %#v", snapshot)
		}
	}
}

func TestRefreshMarksSiteScoreRunFailedWhenTargetFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	target := scoringTarget(1)
	base := newFakeRepository(now, target)
	base.saveErrs[target.SupplierID] = errors.New("write failed")
	repository := &scoreRunRepository{fakeRepository: base}
	service, err := applicationscoring.NewService(repository, func() time.Time { return now }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("refresh should report the target failure")
	}
	if len(repository.runs) != 1 || repository.runs[0].completed != 0 || repository.runs[0].succeeded || repository.runs[0].summary == "" {
		t.Fatalf("unexpected failed score run: %#v", repository.runs)
	}
}
