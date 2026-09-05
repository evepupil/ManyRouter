package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const evaluationQueue = "evaluation"

type EvaluationArgs struct {
	PayloadVersion int    `json:"payload_version"`
	RunID          string `json:"run_id" river:"unique"`
}

func (EvaluationArgs) Kind() string { return "run_model_evaluation_v1" }

type EvaluationSweepArgs struct {
	PayloadVersion int `json:"payload_version"`
}

func (EvaluationSweepArgs) Kind() string { return "schedule_model_evaluations_v1" }

type EvaluationRunner interface {
	ExecuteRun(context.Context, uuid.UUID) error
	ScheduleDue(context.Context) error
}

type EvaluationWorker struct {
	river.WorkerDefaults[EvaluationArgs]
	runner EvaluationRunner
}

func (worker *EvaluationWorker) Work(ctx context.Context, job *river.Job[EvaluationArgs]) error {
	if job.Args.PayloadVersion != 1 {
		return river.JobCancel(errors.New("evaluation job payload version is unsupported"))
	}
	runID, err := uuid.Parse(job.Args.RunID)
	if err != nil || runID == uuid.Nil {
		return river.JobCancel(errors.New("evaluation run ID is invalid"))
	}
	return worker.runner.ExecuteRun(ctx, runID)
}

func (*EvaluationWorker) Timeout(*river.Job[EvaluationArgs]) time.Duration {
	return 3 * time.Hour
}

type EvaluationSweepWorker struct {
	river.WorkerDefaults[EvaluationSweepArgs]
	runner EvaluationRunner
}

func (worker *EvaluationSweepWorker) Work(ctx context.Context, job *river.Job[EvaluationSweepArgs]) error {
	if job.Args.PayloadVersion != 1 {
		return river.JobCancel(errors.New("evaluation sweep payload version is unsupported"))
	}
	return worker.runner.ScheduleDue(ctx)
}

func (*EvaluationSweepWorker) Timeout(*river.Job[EvaluationSweepArgs]) time.Duration {
	return 2 * time.Minute
}

func evaluationPeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return EvaluationSweepArgs{PayloadVersion: 1}, &river.InsertOpts{
					Queue: evaluationQueue, MaxAttempts: 5,
					UniqueOpts: river.UniqueOpts{ByPeriod: 50 * time.Minute},
				}
			},
			&river.PeriodicJobOpts{ID: "m2_evaluation_sweep_v1", RunOnStart: true},
		),
	}
}

func (dispatcher *Dispatcher) DispatchEvaluation(ctx context.Context, runID uuid.UUID) error {
	dispatcher.mutex.RLock()
	client := dispatcher.client
	dispatcher.mutex.RUnlock()
	if client == nil {
		return errors.New("river dispatcher is not bound")
	}
	_, err := client.Insert(ctx, EvaluationArgs{PayloadVersion: 1, RunID: runID.String()}, &river.InsertOpts{
		Queue: evaluationQueue, MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	return err
}
