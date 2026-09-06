package jobs

import (
	"context"
	"errors"
	"time"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	"github.com/riverqueue/river"
)

const automationQueue = "automation"

type AutomationArgs struct {
	PayloadVersion int `json:"payload_version"`
}

func (AutomationArgs) Kind() string { return "apply_fixed_auto_recommendations_v1" }

type AutomationRunner interface {
	ProcessReady(context.Context, automationapp.TriggerKind) ([]automationapp.Run, error)
}

type AutomationWorker struct {
	river.WorkerDefaults[AutomationArgs]
	runner AutomationRunner
}

func (worker *AutomationWorker) Work(ctx context.Context, job *river.Job[AutomationArgs]) error {
	if job.Args.PayloadVersion != 1 {
		return river.JobCancel(errors.New("automation job payload version is unsupported"))
	}
	_, err := worker.runner.ProcessReady(ctx, automationapp.TriggerScheduled)
	return err
}

func (*AutomationWorker) Timeout(*river.Job[AutomationArgs]) time.Duration {
	return 5 * time.Minute
}

func automationPeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) {
				return AutomationArgs{PayloadVersion: 1}, &river.InsertOpts{
					Queue: automationQueue, MaxAttempts: 5,
					UniqueOpts: river.UniqueOpts{ByPeriod: 4 * time.Minute},
				}
			},
			&river.PeriodicJobOpts{ID: "m3_fixed_auto_automation_v1", RunOnStart: false},
		),
	}
}
