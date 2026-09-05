package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/collection"
	"github.com/riverqueue/river"
)

const collectionQueue = "collection"

type CollectionSweepArgs struct {
	PayloadVersion int `json:"payload_version"`
}

func (CollectionSweepArgs) Kind() string { return "collect_measurements_v1" }

type CollectionRunner interface {
	CollectAll(context.Context) (collection.SweepResult, error)
}

type CollectionWorker struct {
	river.WorkerDefaults[CollectionSweepArgs]
	runner CollectionRunner
}

func (worker *CollectionWorker) Work(ctx context.Context, job *river.Job[CollectionSweepArgs]) error {
	if job.Args.PayloadVersion != 1 {
		return river.JobCancel(errors.New("collection job payload version is unsupported"))
	}
	_, err := worker.runner.CollectAll(ctx)
	return err
}

func (*CollectionWorker) Timeout(*river.Job[CollectionSweepArgs]) time.Duration {
	return 3 * time.Minute
}

func collectionPeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) {
				return CollectionSweepArgs{PayloadVersion: 1}, &river.InsertOpts{
					Queue: collectionQueue, MaxAttempts: 5,
					UniqueOpts: river.UniqueOpts{ByPeriod: 4 * time.Minute},
				}
			},
			&river.PeriodicJobOpts{ID: "m2_collection_sweep_v1", RunOnStart: true},
		),
	}
}
