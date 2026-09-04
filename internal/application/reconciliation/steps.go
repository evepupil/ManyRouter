package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Service) recordSuccess(ctx context.Context, operationID uuid.UUID, key string, before, after any, startedAt time.Time) error {
	finishedAt := s.now().UTC()
	return s.store.RecordStep(ctx, StepRecord{
		OperationID: operationID, Key: key, Status: StepSucceeded, Before: before, After: after,
		StartedAt: startedAt, FinishedAt: &finishedAt,
	})
}

func (s *Service) finishFailure(ctx context.Context, bundle Bundle, step string, err error) error {
	failure := classifyFailure(err)
	now := s.now().UTC()
	stepStatus := StepFailed
	if failure.Kind == FailureUncertain {
		stepStatus = StepUncertain
	}
	recordErr := s.store.RecordStep(ctx, StepRecord{
		OperationID: bundle.Operation.ID, Key: step, Status: stepStatus,
		ErrorCode: failure.Code, ErrorMessage: failure.Message, StartedAt: now, FinishedAt: &now,
	})
	var nextAttemptAt *time.Time
	if failure.Kind == FailureRetryable || failure.Kind == FailureUncertain {
		next := now.Add(retryDelay(bundle.Operation.Attempt + 1))
		nextAttemptAt = &next
	}
	persistErr := s.store.FailOperation(ctx, FailureRecord{
		OperationID: bundle.Operation.ID, SiteID: bundle.Operation.SiteID,
		RelationID: bundle.Operation.RelationID, RoutePlanID: bundle.Operation.RoutePlanID,
		Kind: failure.Kind, Step: step, Code: failure.Code, Message: failure.Message,
		NextAttemptAt: nextAttemptAt, OccurredAt: now,
	})
	if recordErr != nil || persistErr != nil {
		joined := []error{failure}
		if recordErr != nil {
			joined = append(joined, fmt.Errorf("record synchronization failure step: %w", recordErr))
		}
		if persistErr != nil {
			joined = append(joined, fmt.Errorf("persist synchronization failure: %w", persistErr))
		}
		return errors.Join(joined...)
	}
	if failure.Kind == FailureRetryable || failure.Kind == FailureUncertain {
		return failure
	}
	return nil
}

func classifyFailure(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	return NewFailure(FailureRetryable, "internal_error", "synchronization could not continue", err)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<(attempt-1)) * 30 * time.Second
}

func copyStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
