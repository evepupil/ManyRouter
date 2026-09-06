package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const reconciliationQueue = "reconciliation"

type clientConfig struct {
	collection CollectionRunner
	evaluation EvaluationRunner
	scoring    ScoringRunner
	automation AutomationRunner
}

type ClientOption func(*clientConfig)

func WithCollection(runner CollectionRunner) ClientOption {
	return func(config *clientConfig) { config.collection = runner }
}

func WithEvaluation(runner EvaluationRunner) ClientOption {
	return func(config *clientConfig) { config.evaluation = runner }
}

func WithScoring(runner ScoringRunner) ClientOption {
	return func(config *clientConfig) { config.scoring = runner }
}

func WithAutomation(runner AutomationRunner) ClientOption {
	return func(config *clientConfig) { config.automation = runner }
}

type ReconciliationArgs struct {
	OperationID string `json:"operation_id"`
}

func (ReconciliationArgs) Kind() string {
	return "reconcile_route_plan_v1"
}

type ReconciliationRunner interface {
	Run(context.Context, uuid.UUID) error
}

type ReconciliationWorker struct {
	river.WorkerDefaults[ReconciliationArgs]
	runner ReconciliationRunner
}

func (w *ReconciliationWorker) Work(ctx context.Context, job *river.Job[ReconciliationArgs]) error {
	operationID, err := uuid.Parse(job.Args.OperationID)
	if err != nil {
		return river.JobCancel(errors.New("synchronization operation ID is invalid"))
	}
	return w.runner.Run(ctx, operationID)
}

func (w *ReconciliationWorker) Timeout(*river.Job[ReconciliationArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *ReconciliationWorker) NextRetry(job *river.Job[ReconciliationArgs]) time.Time {
	attempt := job.Attempt
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Now().Add(time.Duration(1<<(attempt-1)) * 30 * time.Second)
}

type Dispatcher struct {
	mutex  sync.RWMutex
	client *river.Client[pgx.Tx]
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}

func (d *Dispatcher) Bind(client *river.Client[pgx.Tx]) error {
	if client == nil {
		return errors.New("river client is required")
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.client != nil {
		return errors.New("river dispatcher is already bound")
	}
	d.client = client
	return nil
}

func (d *Dispatcher) Dispatch(ctx context.Context, operationID uuid.UUID) error {
	d.mutex.RLock()
	client := d.client
	d.mutex.RUnlock()
	if client == nil {
		return errors.New("river dispatcher is not bound")
	}
	_, err := client.Insert(ctx, ReconciliationArgs{OperationID: operationID.String()}, &river.InsertOpts{
		Queue:       reconciliationQueue,
		MaxAttempts: 10,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	})
	return err
}

func NewClient(pool *pgxpool.Pool, runner ReconciliationRunner, execute bool, options ...ClientOption) (*river.Client[pgx.Tx], error) {
	if pool == nil || runner == nil {
		return nil, errors.New("river pool and reconciliation runner are required")
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &ReconciliationWorker{runner: runner})
	clientOptions := clientConfig{}
	for _, option := range options {
		option(&clientOptions)
	}
	config := &river.Config{Workers: workers}
	if clientOptions.collection != nil {
		river.AddWorker(workers, &CollectionWorker{runner: clientOptions.collection})
		config.PeriodicJobs = collectionPeriodicJobs()
	}
	if clientOptions.evaluation != nil {
		river.AddWorker(workers, &EvaluationWorker{runner: clientOptions.evaluation})
		river.AddWorker(workers, &EvaluationSweepWorker{runner: clientOptions.evaluation})
		config.PeriodicJobs = append(config.PeriodicJobs, evaluationPeriodicJobs()...)
	}
	if clientOptions.scoring != nil {
		river.AddWorker(workers, &ScoringWorker{runner: clientOptions.scoring})
		config.PeriodicJobs = append(config.PeriodicJobs, scoringPeriodicJobs()...)
	}
	if clientOptions.automation != nil {
		river.AddWorker(workers, &AutomationWorker{runner: clientOptions.automation})
		config.PeriodicJobs = append(config.PeriodicJobs, automationPeriodicJobs()...)
	}
	if execute {
		config.Queues = map[string]river.QueueConfig{
			reconciliationQueue: {MaxWorkers: 4},
		}
		if clientOptions.collection != nil {
			config.Queues[collectionQueue] = river.QueueConfig{MaxWorkers: 1}
		}
		if clientOptions.evaluation != nil {
			config.Queues[evaluationQueue] = river.QueueConfig{MaxWorkers: 2}
		}
		if clientOptions.scoring != nil {
			config.Queues[scoringQueue] = river.QueueConfig{MaxWorkers: 1}
		}
		if clientOptions.automation != nil {
			config.Queues[automationQueue] = river.QueueConfig{MaxWorkers: 1}
		}
	}
	return river.NewClient(riverpgxv5.New(pool), config)
}
