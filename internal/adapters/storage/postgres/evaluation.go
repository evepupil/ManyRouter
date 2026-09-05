package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

func (store *Store) ListEvaluationTargets(ctx context.Context) ([]evaluationapp.TargetAccess, error) {
	rows, err := store.queries.ListEvaluationTargets(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]evaluationapp.TargetAccess, 0, len(rows))
	for _, row := range rows {
		result = append(result, evaluationTargetAccess(
			row.SupplierID, row.SupplierName, row.UpstreamBaseUrl, row.Model, row.UpstreamModel,
			row.CredentialID, row.CredentialPurpose, row.Ciphertext, row.Nonce, row.KeyVersion,
		))
	}
	return result, nil
}

func (store *Store) GetEvaluationTarget(ctx context.Context, supplierID uuid.UUID, model string) (evaluationapp.TargetAccess, error) {
	row, err := store.queries.GetEvaluationTarget(ctx, sqlc.GetEvaluationTargetParams{ID: supplierID, Model: model})
	if err != nil {
		return evaluationapp.TargetAccess{}, err
	}
	return evaluationTargetAccess(
		row.SupplierID, row.SupplierName, row.UpstreamBaseUrl, row.Model, row.UpstreamModel,
		row.CredentialID, row.CredentialPurpose, row.Ciphertext, row.Nonce, row.KeyVersion,
	), nil
}

func evaluationTargetAccess(
	supplierID uuid.UUID,
	supplierName string,
	baseURL string,
	model string,
	upstreamModel string,
	credentialID uuid.UUID,
	purpose string,
	ciphertext []byte,
	nonce []byte,
	keyVersion int32,
) evaluationapp.TargetAccess {
	return evaluationapp.TargetAccess{
		SupplierID: supplierID, SupplierName: supplierName, BaseURL: baseURL,
		Model: model, UpstreamModel: upstreamModel,
		Credential: credential.Record{
			ID: credentialID, Purpose: credential.Purpose(purpose),
			Ciphertext: append([]byte(nil), ciphertext...), Nonce: append([]byte(nil), nonce...),
			KeyVersion: keyVersion,
		},
	}
}

func (store *Store) CreateEvaluationRun(ctx context.Context, run evaluationapp.Run, dailyLimit int) (evaluationapp.Run, error) {
	if dailyLimit < 1 || run.PlannedSamples < 1 || run.PlannedSamples > dailyLimit {
		return evaluationapp.Run{}, evaluationapp.ErrDailyBudgetExceeded
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return evaluationapp.Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	if run.RequestKey != "" {
		if err := queries.LockEvaluationRequestKey(ctx, "evaluation_run|"+run.RequestKey); err != nil {
			return evaluationapp.Run{}, err
		}
		existing, err := queries.FindEvaluationRunByRequestKey(ctx, optionalText(run.RequestKey))
		if err == nil {
			if textValue(existing.RequestHash) != run.RequestHash {
				return evaluationapp.Run{}, evaluationapp.ErrRequestKeyReused
			}
			mapped, mapErr := mapEvaluationRun(existing)
			if mapErr != nil {
				return evaluationapp.Run{}, mapErr
			}
			if err := tx.Commit(ctx); err != nil {
				return evaluationapp.Run{}, err
			}
			mapped.SupplierName = run.SupplierName
			return mapped, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return evaluationapp.Run{}, err
		}
	}
	budgetDate := pgtype.Date{Time: startOfDatabaseDate(run.RequestedAt), Valid: true}
	if err := queries.EnsureEvaluationDailyBudget(ctx, sqlc.EnsureEvaluationDailyBudgetParams{
		SupplierID: run.SupplierID, Model: run.Model, BucketDate: budgetDate,
		UpdatedAt: databaseTime(run.RequestedAt),
	}); err != nil {
		return evaluationapp.Run{}, err
	}
	budget, err := queries.LockEvaluationDailyBudget(ctx, sqlc.LockEvaluationDailyBudgetParams{
		SupplierID: run.SupplierID, Model: run.Model, BucketDate: budgetDate,
	})
	if err != nil {
		return evaluationapp.Run{}, err
	}
	if int64(budget.ReservedSamples)+int64(run.PlannedSamples) > int64(dailyLimit) {
		return evaluationapp.Run{}, evaluationapp.ErrDailyBudgetExceeded
	}
	if err := queries.ReserveEvaluationDailyBudget(ctx, sqlc.ReserveEvaluationDailyBudgetParams{
		SupplierID: run.SupplierID, Model: run.Model, BucketDate: budgetDate,
		ReservedSamples: int32(run.PlannedSamples), UpdatedAt: databaseTime(run.RequestedAt),
	}); err != nil {
		return evaluationapp.Run{}, err
	}
	row, err := queries.CreateEvaluationRun(ctx, sqlc.CreateEvaluationRunParams{
		ID: run.ID, SupplierID: run.SupplierID, RelationID: databaseUUIDPointer(run.RelationID),
		SiteID: databaseUUIDPointer(run.SiteID), Model: run.Model, UpstreamModel: run.UpstreamModel,
		TargetKind: string(run.TargetKind), Purpose: string(run.Purpose), SuiteVersion: run.SuiteVersion,
		AlgorithmVersion: run.AlgorithmVersion, RandomSeed: int64(run.Seed),
		ReferenceID: databaseUUIDPointer(run.ReferenceID), PlannedSamples: int32(run.PlannedSamples),
		RequestedBy: run.RequestedBy, RequestReason: run.RequestReason, RequestedAt: databaseTime(run.RequestedAt),
		RequestKey: optionalText(run.RequestKey), RequestHash: optionalText(run.RequestHash),
	})
	if err != nil {
		return evaluationapp.Run{}, err
	}
	if err := queries.CreateEvaluationBudgetReservation(ctx, sqlc.CreateEvaluationBudgetReservationParams{
		RunID: run.ID, SupplierID: run.SupplierID, Model: run.Model, BucketDate: budgetDate,
		PlannedSamples: int32(run.PlannedSamples), CreatedAt: databaseTime(run.RequestedAt),
	}); err != nil {
		return evaluationapp.Run{}, err
	}
	mapped, err := mapEvaluationRun(row)
	if err != nil {
		return evaluationapp.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return evaluationapp.Run{}, err
	}
	mapped.SupplierName = run.SupplierName
	return mapped, nil
}

func (store *Store) FindEvaluationRunByRequestKey(ctx context.Context, key string) (*evaluationapp.Run, error) {
	row, err := store.queries.FindEvaluationRunByRequestKey(ctx, optionalText(key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run, err := mapEvaluationRun(row)
	return &run, err
}

func (store *Store) GetEvaluationRun(ctx context.Context, runID uuid.UUID) (evaluationapp.Run, error) {
	row, err := store.queries.GetEvaluationRun(ctx, runID)
	if err != nil {
		return evaluationapp.Run{}, err
	}
	return mapEvaluationRun(row)
}

func (store *Store) FindRecentEvaluationRun(
	ctx context.Context,
	supplierID uuid.UUID,
	model string,
	target evaluationapp.TargetKind,
	purpose evaluationapp.Purpose,
	since time.Time,
) (*evaluationapp.Run, error) {
	row, err := store.queries.FindRecentEvaluationRun(ctx, sqlc.FindRecentEvaluationRunParams{
		SupplierID: supplierID, Model: model, TargetKind: string(target), Purpose: string(purpose),
		RequestedAt: databaseTime(since),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	mapped, err := mapEvaluationRun(row)
	return &mapped, err
}

func (store *Store) ListEvaluationRuns(ctx context.Context, filter evaluationapp.RunFilter) (evaluationapp.RunPage, error) {
	rows, err := store.queries.ListEvaluationRuns(ctx, sqlc.ListEvaluationRunsParams{
		SiteID: databaseUUIDPointer(filter.SiteID), SupplierID: databaseUUIDPointer(filter.SupplierID),
		Model: optionalText(filter.Model), Purpose: optionalText(string(filter.Purpose)),
		PageLimit: int32(filter.Limit), PageOffset: int32(filter.Offset),
	})
	if err != nil {
		return evaluationapp.RunPage{}, err
	}
	page := evaluationapp.RunPage{Items: make([]evaluationapp.Run, 0, len(rows)), Limit: filter.Limit, Offset: filter.Offset}
	for _, row := range rows {
		if !row.RequestedAt.Valid {
			return evaluationapp.RunPage{}, errors.New("evaluation run request time is invalid")
		}
		page.Items = append(page.Items, evaluationapp.Run{
			ID: row.ID, SupplierID: row.SupplierID, SupplierName: row.SupplierName,
			RelationID: optionalUUID(row.RelationID), SiteID: optionalUUID(row.SiteID),
			Model: row.Model, UpstreamModel: row.UpstreamModel,
			TargetKind: evaluationapp.TargetKind(row.TargetKind), Purpose: evaluationapp.Purpose(row.Purpose),
			Status: evaluationapp.RunStatus(row.Status), SuiteVersion: row.SuiteVersion,
			AlgorithmVersion: row.AlgorithmVersion, Seed: uint64(row.RandomSeed), ReferenceID: optionalUUID(row.ReferenceID),
			PlannedSamples: int(row.PlannedSamples), CompletedSamples: int(row.CompletedSamples),
			RequestedBy: row.RequestedBy, RequestReason: row.RequestReason,
			ErrorCode: textValue(row.ErrorCode), ErrorMessage: textValue(row.ErrorMessage),
			RequestedAt: row.RequestedAt.Time.UTC(), StartedAt: optionalTime(row.StartedAt), CompletedAt: optionalTime(row.CompletedAt),
			NextRetryAt: optionalTime(row.NextRetryAt), RequestKey: textValue(row.RequestKey), RequestHash: textValue(row.RequestHash),
		})
		page.Total = row.TotalCount
	}
	return page, nil
}

func mapEvaluationRun(row sqlc.EvaluationRun) (evaluationapp.Run, error) {
	if !row.RequestedAt.Valid {
		return evaluationapp.Run{}, errors.New("evaluation run request time is invalid")
	}
	return evaluationapp.Run{
		ID: row.ID, SupplierID: row.SupplierID, RelationID: optionalUUID(row.RelationID), SiteID: optionalUUID(row.SiteID),
		Model: row.Model, UpstreamModel: row.UpstreamModel, TargetKind: evaluationapp.TargetKind(row.TargetKind),
		Purpose: evaluationapp.Purpose(row.Purpose), Status: evaluationapp.RunStatus(row.Status),
		SuiteVersion: row.SuiteVersion, AlgorithmVersion: row.AlgorithmVersion, Seed: uint64(row.RandomSeed),
		ReferenceID: optionalUUID(row.ReferenceID), PlannedSamples: int(row.PlannedSamples),
		CompletedSamples: int(row.CompletedSamples), RequestedBy: row.RequestedBy,
		RequestReason: row.RequestReason, ErrorCode: textValue(row.ErrorCode), ErrorMessage: textValue(row.ErrorMessage),
		RequestedAt: row.RequestedAt.Time.UTC(), StartedAt: optionalTime(row.StartedAt), CompletedAt: optionalTime(row.CompletedAt),
		NextRetryAt: optionalTime(row.NextRetryAt), RequestKey: textValue(row.RequestKey), RequestHash: textValue(row.RequestHash),
	}, nil
}

func (store *Store) StartEvaluationRun(ctx context.Context, runID uuid.UUID, now time.Time) (bool, error) {
	rows, err := store.queries.StartEvaluationRun(ctx, sqlc.StartEvaluationRunParams{ID: runID, StartedAt: databaseTime(now)})
	return rows > 0, err
}

func (store *Store) ListEvaluationSamples(ctx context.Context, runID uuid.UUID) ([]evaluationapp.Sample, error) {
	rows, err := store.queries.ListEvaluationSamples(ctx, runID)
	if err != nil {
		return nil, err
	}
	result := make([]evaluationapp.Sample, 0, len(rows))
	for _, row := range rows {
		if !row.CollectedAt.Valid {
			return nil, errors.New("evaluation sample time is invalid")
		}
		result = append(result, evaluationapp.Sample{
			RunID: row.RunID, ProbeKey: row.ProbeKey, SampleIndex: int(row.SampleIndex),
			PromptVariant: int(row.PromptVariant), Outcome: row.Outcome,
			NormalizedAnswer: textValue(row.NormalizedAnswer), AnswerDigest: textValue(row.AnswerDigest),
			ResponseModel: textValue(row.ResponseModel), HTTPStatus: intValue(row.UpstreamStatusCode),
			FirstTokenMillis: int64Pointer(row.TtftMs), TotalMillis: int64Pointer(row.DurationMs),
			InputTokens: int64Value(row.InputTokens), OutputTokens: int64Value(row.OutputTokens),
			StreamCompleted: boolPointer(row.StreamCompleted),
			Error: measurement.ErrorFact{
				Class: measurement.ErrorClass(textValue(row.ErrorCategory)), StableCode: textValue(row.ErrorCode),
				RuleVersion: row.ClassificationVersion,
			},
			CollectedAt: row.CollectedAt.Time.UTC(),
		})
	}
	return result, nil
}

func (store *Store) ReserveEvaluationSample(ctx context.Context, sample evaluationapp.Sample) (bool, error) {
	inserted, err := store.queries.ReserveEvaluationSample(ctx, sqlc.ReserveEvaluationSampleParams{
		RunID: sample.RunID, ProbeKey: sample.ProbeKey, SampleIndex: int32(sample.SampleIndex),
		PromptVariant: int32(sample.PromptVariant), ClassificationVersion: measurement.ErrorClassificationRuleVersion,
		CollectedAt: databaseTime(sample.CollectedAt),
	})
	return inserted == 1, err
}

func (store *Store) CompleteEvaluationSample(
	ctx context.Context,
	sample evaluationapp.Sample,
	requestFact measurement.RequestFact,
	attemptFact measurement.AttemptFact,
) error {
	if err := requestFact.Validate(); err != nil {
		return err
	}
	if err := attemptFact.Validate(); err != nil {
		return err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	measurementID := deterministicMeasurementID("request", requestFact.SiteID, requestFact.SourceHash)
	insertedRequest, err := insertMeasurementRequestRevision(
		ctx, queries, measurementID, requestFact, evaluationapp.AlgorithmVersion, 1, sample.CollectedAt,
	)
	if err != nil {
		return err
	}
	if insertedRequest != 1 {
		return errors.New("evaluation measurement request already exists")
	}
	insertedAttempt, err := queries.InsertMeasurementAttempt(ctx, sqlc.InsertMeasurementAttemptParams{
		ID:                   measurementAttemptID(attemptFact.SiteID, measurementID, attemptFact.SourceHash),
		RequestMeasurementID: measurementID, AttemptIndex: int32(attemptFact.Ordinal),
		RelationID: databaseUUID(attemptFact.Attribution.RelationID), SupplierID: databaseUUID(attemptFact.Attribution.SupplierID),
		ExternalChannelID: optionalPositiveInt64(attemptFact.ChannelID), AttributionStatus: string(attemptFact.Attribution.Status),
		Model: attemptFact.Model, Outcome: string(attemptFact.Result), IsFinal: attemptFact.IsFinal,
		IsStream: attemptFact.IsStream, StreamCompleted: streamCompletionValue(attemptFact.IsStream, attemptFact.StreamCompletion),
		ProducedVisibleOutput: pgtype.Bool{Bool: attemptFact.ProducedVisibleOutput, Valid: true},
		TtftMs:                optionalInt64(attemptFact.FirstTokenMillis), DurationMs: optionalInt64(attemptFact.TotalMillis),
		DurationResolutionMs: optionalDurationResolution(attemptFact.TotalMillis, attemptFact.DurationResolutionMillis),
		UpstreamStatusCode:   optionalHTTPStatus(attemptFact.HTTPStatus),
		ErrorCategory:        measurementErrorClass(attemptFact.Error), ErrorResponsibility: measurementErrorResponsibility(attemptFact.Error),
		ErrorCode:             optionalText(attemptFact.Error.StableCode),
		ClassificationVersion: measurement.ErrorClassificationRuleVersion,
		ObservedAt:            databaseTime(attemptFact.OccurredAt), RecordedAt: databaseTime(sample.CollectedAt),
	})
	if err != nil {
		return err
	}
	if insertedAttempt != 1 {
		return errors.New("evaluation measurement attempt already exists")
	}
	shape, err := json.Marshal(map[string]any{
		"finish_reason": sample.FinishReason,
		"stream":        sample.Stream,
	})
	if err != nil {
		return err
	}
	completed, err := queries.CompleteEvaluationSample(ctx, sqlc.CompleteEvaluationSampleParams{
		RunID: sample.RunID, ProbeKey: sample.ProbeKey, SampleIndex: int32(sample.SampleIndex),
		Outcome:          sample.Outcome,
		NormalizedAnswer: optionalText(sample.NormalizedAnswer), AnswerDigest: optionalText(sample.AnswerDigest),
		ResponseModel: optionalText(sample.ResponseModel), ResponseShape: shape,
		TtftMs: optionalInt64(sample.FirstTokenMillis), DurationMs: optionalInt64(sample.TotalMillis),
		InputTokens:     pgtype.Int8{Int64: sample.InputTokens, Valid: true},
		OutputTokens:    pgtype.Int8{Int64: sample.OutputTokens, Valid: true},
		StreamCompleted: optionalBoolPointer(sample.StreamCompleted), UpstreamStatusCode: optionalHTTPStatus(sample.HTTPStatus),
		ErrorCategory: measurementErrorClass(sample.Error), ErrorCode: optionalText(sample.Error.StableCode),
		ClassificationVersion: measurement.ErrorClassificationRuleVersion,
		MeasurementRequestID:  databaseUUID(measurementID), CollectedAt: databaseTime(sample.CollectedAt),
	})
	if err != nil {
		return err
	}
	if completed != 1 {
		return errors.New("evaluation sample reservation is missing or already completed")
	}
	if err := queries.AdvanceEvaluationRun(ctx, sample.RunID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (store *Store) SaveEvaluationFingerprint(ctx context.Context, fingerprint domainevaluation.Fingerprint) error {
	runID, err := uuid.Parse(fingerprint.RunID)
	if err != nil {
		return errors.New("evaluation fingerprint run ID is invalid")
	}
	cells, err := json.Marshal(fingerprint.Cells)
	if err != nil {
		return err
	}
	validCells, validSamples := 0, 0
	for _, distribution := range fingerprint.Cells {
		if distribution.ValidSamples() > 0 {
			validCells++
			validSamples += int(distribution.ValidSamples())
		}
	}
	return store.queries.CreateEvaluationFingerprint(ctx, sqlc.CreateEvaluationFingerprintParams{
		RunID: runID, ProtocolVersion: fingerprint.ProtocolVersion, Cells: cells,
		ValidCells: int32(validCells), ValidSamples: int32(validSamples),
		SelfDistance: optionalNumeric(fingerprint.Stability.Distance, fingerprint.Stability.Measured),
		Stable:       fingerprint.Stability.Measured && fingerprint.Stability.Distance <= domainevaluation.DefaultAuthenticityPolicy().MaximumSelfDistance,
		CreatedAt:    databaseTime(fingerprint.CollectedAt),
	})
}

func (store *Store) GetEvaluationFingerprint(ctx context.Context, runID uuid.UUID) (domainevaluation.Fingerprint, error) {
	row, err := store.queries.GetEvaluationFingerprint(ctx, runID)
	if err != nil {
		return domainevaluation.Fingerprint{}, err
	}
	return store.mapEvaluationFingerprint(ctx, row)
}

func (store *Store) mapEvaluationFingerprint(ctx context.Context, row sqlc.EvaluationFingerprint) (domainevaluation.Fingerprint, error) {
	if !row.CreatedAt.Valid {
		return domainevaluation.Fingerprint{}, errors.New("evaluation fingerprint time is invalid")
	}
	cells := make(map[domainevaluation.CellID]domainevaluation.Distribution)
	if err := json.Unmarshal(row.Cells, &cells); err != nil {
		return domainevaluation.Fingerprint{}, err
	}
	selfDistance, measured, err := numericFloat(row.SelfDistance)
	if err != nil {
		return domainevaluation.Fingerprint{}, err
	}
	run, err := store.GetEvaluationRun(ctx, row.RunID)
	if err != nil {
		return domainevaluation.Fingerprint{}, err
	}
	return domainevaluation.Fingerprint{
		RunID: row.RunID.String(), Seed: run.Seed, ProtocolVersion: row.ProtocolVersion,
		CollectedAt: row.CreatedAt.Time.UTC(), Cells: cells,
		Stability: domainevaluation.Stability{Measured: measured, Distance: selfDistance},
	}, nil
}

func (store *Store) FindTrustedReference(ctx context.Context, model string, now time.Time) (*domainevaluation.ModelReference, error) {
	row, err := store.queries.FindTrustedModelReference(ctx, sqlc.FindTrustedModelReferenceParams{Model: model, ExpiresAt: databaseTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reference, err := store.mapTrustedReference(ctx, row)
	return &reference, err
}

func (store *Store) GetTrustedReference(ctx context.Context, referenceID uuid.UUID) (domainevaluation.ModelReference, error) {
	row, err := store.queries.GetTrustedModelReference(ctx, referenceID)
	if err != nil {
		return domainevaluation.ModelReference{}, err
	}
	return store.mapTrustedReference(ctx, row)
}

func (store *Store) mapTrustedReference(ctx context.Context, row sqlc.TrustedModelReference) (domainevaluation.ModelReference, error) {
	fingerprint, err := store.GetEvaluationFingerprint(ctx, row.FingerprintRunID)
	if err != nil {
		return domainevaluation.ModelReference{}, err
	}
	if !row.ExpiresAt.Valid {
		return domainevaluation.ModelReference{}, errors.New("trusted reference expiry is invalid")
	}
	expiresAt := row.ExpiresAt.Time.UTC()
	if row.RevokedAt.Valid && row.RevokedAt.Time.Before(expiresAt) {
		expiresAt = row.RevokedAt.Time.UTC()
	}
	return domainevaluation.ModelReference{
		ID: row.ID.String(), Revision: 1, Trust: domainevaluation.ReferenceTrust(row.TrustLevel),
		Source:    domainevaluation.ModelSubject{SupplierID: row.SupplierID, Model: row.Model},
		ExpiresAt: expiresAt, Fingerprint: fingerprint,
	}, nil
}

func (store *Store) FindPreviousMismatch(
	ctx context.Context,
	subject domainevaluation.ModelSubject,
	reference domainevaluation.ModelReference,
	minimumDistance float64,
	before time.Time,
) (*domainevaluation.MismatchEvidence, error) {
	referenceID, err := uuid.Parse(reference.ID)
	if err != nil {
		return nil, errors.New("trusted reference identity is invalid")
	}
	row, err := store.queries.FindPreviousStableMismatch(ctx, sqlc.FindPreviousStableMismatchParams{
		SupplierID: subject.SupplierID, Model: subject.Model, ReferenceID: databaseUUID(referenceID),
		MeanDistance: numericValue(minimumDistance), CheckedAt: databaseTime(before),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fingerprint, err := store.GetEvaluationFingerprint(ctx, row.RunID)
	if err != nil {
		return nil, err
	}
	distance, ok, err := numericFloat(row.MeanDistance)
	if err != nil || !ok {
		return nil, errors.New("previous mismatch distance is invalid")
	}
	return &domainevaluation.MismatchEvidence{
		Subject: subject, TargetRunID: row.RunID.String(), TargetSeed: fingerprint.Seed,
		ObservedAt: fingerprint.CollectedAt, ReferenceID: reference.ID, ReferenceRevision: reference.Revision,
		ProtocolVersion: fingerprint.ProtocolVersion, Distance: distance,
		SelfDistance: fingerprint.Stability.Distance,
	}, nil
}

func (store *Store) SaveAuthenticityAssessment(
	ctx context.Context,
	runID uuid.UUID,
	subject domainevaluation.ModelSubject,
	referenceID *uuid.UUID,
	assessment domainevaluation.AuthenticityAssessment,
	evidenceConflict bool,
	now time.Time,
) error {
	evidence, err := json.Marshal(map[string]any{
		"reason": assessment.Reason, "cell_distances": assessment.Comparison.CellDistances,
	})
	if err != nil {
		return err
	}
	selfDistance, err := store.authenticitySelfDistance(ctx, runID)
	if err != nil {
		return err
	}
	err = store.queries.CreateAuthenticityAssessment(ctx, sqlc.CreateAuthenticityAssessmentParams{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("authenticity|"+runID.String())), RunID: runID,
		SupplierID: subject.SupplierID, Model: subject.Model, Verdict: string(assessment.Verdict),
		Confidence:      decimal.NewFromFloat(authenticityConfidence(assessment.Confidence)),
		ReferenceID:     databaseUUIDPointer(referenceID),
		MeanDistance:    optionalNumeric(assessment.Comparison.MeanDistance, assessment.Comparison.Comparable),
		SelfDistance:    selfDistance,
		ComparableCells: int32(assessment.Comparison.ComparableCells), EvidenceConflict: evidenceConflict,
		Evidence: evidence, AlgorithmVersion: assessment.PolicyVersion,
		CheckedAt: databaseTime(now), ExpiresAt: databaseTime(now.Add(7 * 24 * time.Hour)),
	})
	return err
}

func (store *Store) authenticitySelfDistance(ctx context.Context, runID uuid.UUID) (pgtype.Numeric, error) {
	fingerprint, err := store.GetEvaluationFingerprint(ctx, runID)
	if err != nil {
		return pgtype.Numeric{}, err
	}
	if !fingerprint.Stability.Measured {
		return pgtype.Numeric{}, nil
	}
	return numericValue(fingerprint.Stability.Distance), nil
}

func (store *Store) SaveCapabilityAssessment(
	ctx context.Context,
	runID uuid.UUID,
	subject domainevaluation.ModelSubject,
	score float64,
	confidence float64,
	completed int,
	total int,
	suiteVersion string,
	now time.Time,
) error {
	ttl := 24 * time.Hour
	if suiteVersion == evaluationapp.CapabilitySuiteVersion {
		ttl = 7 * 24 * time.Hour
	} else if suiteVersion != evaluationapp.HealthSuiteVersion {
		return errors.New("capability assessment suite is unsupported")
	}
	err := store.queries.CreateCapabilityAssessment(ctx, sqlc.CreateCapabilityAssessmentParams{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("capability|"+runID.String())), RunID: runID,
		SupplierID: subject.SupplierID, Model: subject.Model, Score: decimal.NewFromFloat(score),
		Confidence: decimal.NewFromFloat(confidence), CompletedChecks: int32(completed), TotalChecks: int32(total),
		SuiteVersion: suiteVersion, CheckedAt: databaseTime(now), ExpiresAt: databaseTime(now.Add(ttl)),
	})
	return err
}

func (store *Store) CompleteEvaluationRun(ctx context.Context, runID uuid.UUID, now time.Time) error {
	rows, err := store.queries.CompleteEvaluationRun(ctx, sqlc.CompleteEvaluationRunParams{RunID: runID, CompletedAt: databaseTime(now)})
	if err == nil && rows == 0 {
		return errors.New("evaluation run is no longer running")
	}
	return err
}

func (store *Store) FailEvaluationRun(
	ctx context.Context,
	runID uuid.UUID,
	status evaluationapp.RunStatus,
	code string,
	message string,
	retryAt *time.Time,
	now time.Time,
) error {
	_, err := store.queries.FailEvaluationRun(ctx, sqlc.FailEvaluationRunParams{
		RunID: runID, Status: string(status), ErrorCode: optionalText(code), ErrorMessage: optionalText(message),
		CompletedAt: databaseTime(now), NextRetryAt: optionalDatabaseTime(retryAt),
	})
	return err
}

func (store *Store) CreateTrustedReference(
	ctx context.Context,
	referenceID uuid.UUID,
	run evaluationapp.Run,
	trust domainevaluation.ReferenceTrust,
	reason string,
	actor string,
	createdAt time.Time,
	expiresAt time.Time,
	requestKey string,
	requestHash string,
) (domainevaluation.ModelReference, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domainevaluation.ModelReference{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	if err := queries.LockEvaluationRequestKey(ctx, "trusted_reference_subject|"+run.SupplierID.String()+"|"+run.Model); err != nil {
		return domainevaluation.ModelReference{}, err
	}
	if requestKey != "" {
		if err := queries.LockEvaluationRequestKey(ctx, "trusted_reference_request|"+requestKey); err != nil {
			return domainevaluation.ModelReference{}, err
		}
		existing, err := queries.FindTrustedModelReferenceByRequestKey(ctx, optionalText(requestKey))
		if err == nil {
			if textValue(existing.RequestHash) != requestHash {
				return domainevaluation.ModelReference{}, evaluationapp.ErrRequestKeyReused
			}
			if err := tx.Commit(ctx); err != nil {
				return domainevaluation.ModelReference{}, err
			}
			return store.mapTrustedReference(ctx, existing)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domainevaluation.ModelReference{}, err
		}
	}
	if err := queries.RevokeTrustedModelReferences(ctx, sqlc.RevokeTrustedModelReferencesParams{
		Model: run.Model, SupplierID: run.SupplierID, RevokedAt: databaseTime(createdAt),
	}); err != nil {
		return domainevaluation.ModelReference{}, err
	}
	row, err := queries.CreateTrustedModelReference(ctx, sqlc.CreateTrustedModelReferenceParams{
		ID: referenceID, Model: run.Model, SupplierID: run.SupplierID, FingerprintRunID: run.ID,
		TrustLevel: string(trust), ProtocolVersion: domainevaluation.ProtocolSingleTokenJSDV1,
		Reason: reason, CreatedBy: actor, CreatedAt: databaseTime(createdAt), ExpiresAt: databaseTime(expiresAt),
		RequestKey: optionalText(requestKey), RequestHash: optionalText(requestHash),
	})
	if err != nil {
		return domainevaluation.ModelReference{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domainevaluation.ModelReference{}, err
	}
	return store.mapTrustedReference(ctx, row)
}

func databaseUUIDPointer(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return databaseUUID(*value)
}

func startOfDatabaseDate(at time.Time) time.Time {
	year, month, day := at.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func optionalBoolPointer(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func int64Pointer(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	copyOfValue := value.Int64
	return &copyOfValue
}

func boolPointer(value pgtype.Bool) *bool {
	if !value.Valid {
		return nil
	}
	copyOfValue := value.Bool
	return &copyOfValue
}

func int64Value(value pgtype.Int8) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func intValue(value pgtype.Int4) int {
	if !value.Valid {
		return 0
	}
	return int(value.Int32)
}

func numericValue(value float64) pgtype.Numeric {
	number := decimal.NewFromFloat(value)
	return pgtype.Numeric{Int: number.Coefficient(), Exp: number.Exponent(), Valid: true}
}

func optionalNumeric(value float64, valid bool) pgtype.Numeric {
	if !valid {
		return pgtype.Numeric{}
	}
	return numericValue(value)
}

func numericFloat(value pgtype.Numeric) (float64, bool, error) {
	if !value.Valid {
		return 0, false, nil
	}
	databaseValue, err := value.Value()
	if err != nil {
		return 0, false, err
	}
	parsed, err := strconv.ParseFloat(fmt.Sprint(databaseValue), 64)
	return parsed, err == nil, err
}

func authenticityConfidence(value domainevaluation.Confidence) float64 {
	switch value {
	case domainevaluation.ConfidenceHigh:
		return 0.95
	case domainevaluation.ConfidenceMedium:
		return 0.75
	case domainevaluation.ConfidenceLow:
		return 0.40
	default:
		return 0
	}
}

var _ evaluationapp.Store = (*Store)(nil)
