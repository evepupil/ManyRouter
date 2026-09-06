package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/riverqueue/river"
)

const compatibilityQueue = "compatibility"

type CompatibilityArgs struct {
	PayloadVersion int `json:"payload_version"`
}

func (CompatibilityArgs) Kind() string { return "check_site_compatibility_v1" }

type CompatibilityRunner interface {
	CheckAll(context.Context) error
}

type CompatibilityWorker struct {
	river.WorkerDefaults[CompatibilityArgs]
	runner CompatibilityRunner
}

func (worker *CompatibilityWorker) Work(ctx context.Context, job *river.Job[CompatibilityArgs]) error {
	if job.Args.PayloadVersion != 1 {
		return river.JobCancel(errors.New("compatibility job payload version is unsupported"))
	}
	return worker.runner.CheckAll(ctx)
}

func (*CompatibilityWorker) Timeout(*river.Job[CompatibilityArgs]) time.Duration {
	return 5 * time.Minute
}

func compatibilityPeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(15*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) {
				return CompatibilityArgs{PayloadVersion: 1}, &river.InsertOpts{
					Queue: compatibilityQueue, MaxAttempts: 5,
					UniqueOpts: river.UniqueOpts{ByPeriod: 14 * time.Minute},
				}
			},
			&river.PeriodicJobOpts{ID: "m4_site_compatibility_v1", RunOnStart: true},
		),
	}
}
