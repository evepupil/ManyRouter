package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	scoringapp "github.com/evepupil/ManyRouter/internal/application/scoring"
	"github.com/google/uuid"
)

func (store *Store) BeginScoreRun(ctx context.Context, run scoringapp.ScoreRun) (bool, error) {
	if run.ID == uuid.Nil || run.SiteID == uuid.Nil || strings.TrimSpace(run.PolicyVersion) == "" ||
		run.WindowEnd.IsZero() || run.StartedAt.IsZero() || run.ExpectedTargets < 1 {
		return false, errors.New("score run is invalid")
	}
	command, err := store.pool.Exec(ctx, `
		INSERT INTO score_runs(
			id,site_id,policy_version,window_end,expected_targets,completed_targets,status,started_at
		) VALUES($1,$2,$3,$4,$5,0,'running',$6)
		ON CONFLICT(site_id,policy_version,window_end) DO NOTHING
	`, run.ID, run.SiteID, run.PolicyVersion, run.WindowEnd.UTC(), run.ExpectedTargets, run.StartedAt.UTC())
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (store *Store) FinishScoreRun(
	ctx context.Context,
	runID uuid.UUID,
	completed int,
	succeeded bool,
	errorSummary string,
	completedAt time.Time,
) error {
	if runID == uuid.Nil || completed < 0 || completedAt.IsZero() {
		return errors.New("score run completion is invalid")
	}
	status := "failed"
	if succeeded {
		status = "succeeded"
		errorSummary = ""
	}
	command, err := store.pool.Exec(ctx, `
		UPDATE score_runs
		SET completed_targets=$2,status=$3,error_summary=NULLIF($4,''),completed_at=$5
		WHERE id=$1 AND status='running'
	`, runID, completed, status, strings.TrimSpace(errorSummary), completedAt.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("score run is no longer active")
	}
	return nil
}

var _ scoringapp.ScoreRunRecorder = (*Store)(nil)
