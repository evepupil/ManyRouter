package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/riverqueue/river"
)

const scoringQueue = "scoring"

type ScoringArgs struct {
	PayloadVersion int `json:"payload_version"`
}

func (ScoringArgs) Kind() string { return "refresh_shadow_scores_v1" }

type ScoringRunner interface {
	Refresh(context.Context) error
}

type ScoringWorker struct {
	river.WorkerDefaults[ScoringArgs]
	runner ScoringRunner
}

func (worker *ScoringWorker) Work(ctx context.Context, job *river.Job[ScoringArgs]) error {
	if job.Args.PayloadVersion != 1 {
		return river.JobCancel(errors.New("scoring job payload version is unsupported"))
	}
	return worker.runner.Refresh(ctx)
}

func (*ScoringWorker) Timeout(*river.Job[ScoringArgs]) time.Duration {
	return 5 * time.Minute
}

func scoringPeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) {
				return ScoringArgs{PayloadVersion: 1}, &river.InsertOpts{
					Queue: scoringQueue, MaxAttempts: 5,
					UniqueOpts: river.UniqueOpts{ByPeriod: 4 * time.Minute},
				}
			},
			&river.PeriodicJobOpts{ID: "m2_shadow_scoring_v1", RunOnStart: true},
		),
	}
}
