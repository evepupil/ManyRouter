package automation_test

import (
	"context"
	"testing"
	"time"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	domainautomation "github.com/evepupil/ManyRouter/internal/domain/automation"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
)

type fakeRepository struct {
	input    automationapp.Input
	recorded []automationapp.ApplyCommand
	applied  []automationapp.ApplyCommand
	setting  automationapp.Setting
}

func (repository *fakeRepository) ListReadyScoreRuns(context.Context, int) ([]automationapp.ScoreRun, error) {
	return []automationapp.ScoreRun{repository.input.ScoreRun}, nil
}

func (repository *fakeRepository) GetLatestSuccessfulScoreRun(context.Context, uuid.UUID) (automationapp.ScoreRun, error) {
	return repository.input.ScoreRun, nil
}

func (repository *fakeRepository) LoadAutomationInput(context.Context, uuid.UUID) (automationapp.Input, error) {
	return repository.input, nil
}

func (repository *fakeRepository) RecordAutomationRun(_ context.Context, command automationapp.ApplyCommand) (automationapp.Run, error) {
	repository.recorded = append(repository.recorded, command)
	return runFromCommand(command), nil
}

func (repository *fakeRepository) ApplyAutomationRun(_ context.Context, command automationapp.ApplyCommand) (automationapp.Run, error) {
	repository.applied = append(repository.applied, command)
	return runFromCommand(command), nil
}

func (repository *fakeRepository) ListAutomationSettings(context.Context, uuid.UUID) ([]automationapp.Setting, error) {
	return []automationapp.Setting{repository.setting}, nil
}

func (repository *fakeRepository) UpdateAutomationSetting(_ context.Context, _ automationapp.UpdateSettingCommand, _ time.Time) (automationapp.Setting, error) {
	return repository.setting, nil
}

func (repository *fakeRepository) ListAutomationRuns(context.Context, automationapp.RunFilter) (automationapp.RunPage, error) {
	return automationapp.RunPage{}, nil
}

type fakeCompatibility struct {
	result automationapp.Compatibility
}

func (compatibility fakeCompatibility) CheckAutomationCompatibility(context.Context, uuid.UUID) (automationapp.Compatibility, error) {
	return compatibility.result, nil
}

func TestAutomaticStrategyCreatesVersionedMembershipChange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{input: automaticInput(now, automationapp.ModeAutomatic, false, domainscoring.AdviceJoin, domainscoring.AdviceJoin)}
	service := newAutomationService(t, repository, now, true)
	run, err := service.Process(context.Background(), repository.input.ScoreRun.ID, automationapp.TriggerScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != automationapp.RunPendingSync || len(repository.applied) != 1 || len(repository.recorded) != 0 {
		t.Fatalf("unexpected run: %#v", run)
	}
	command := repository.applied[0]
	if len(command.Strategies) != 1 || len(command.Strategies[0].MemberRelationIDs) != 1 || len(command.Decisions) != 1 {
		t.Fatalf("unexpected apply command: %#v", command)
	}
	if command.Decisions[0].Action != domainautomation.ActionJoin {
		t.Fatalf("unexpected decision: %#v", command.Decisions[0])
	}
}

func TestManualStrategyProducesPreviewOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{input: automaticInput(now, automationapp.ModeManual, false, domainscoring.AdviceJoin)}
	service := newAutomationService(t, repository, now, true)
	run, err := service.Process(context.Background(), repository.input.ScoreRun.ID, automationapp.TriggerOperator)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != automationapp.RunPreview || len(repository.recorded) != 1 || len(repository.applied) != 0 || len(repository.recorded[0].Decisions) != 1 {
		t.Fatalf("unexpected preview: %#v", run)
	}
}

func TestStaleScoreAndIncompatibleGatewayFreezeAutomaticChanges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		windowEnd  time.Time
		compatible bool
	}{
		{name: "stale score", windowEnd: now.Add(-16 * time.Minute), compatible: true},
		{name: "retry disabled", windowEnd: now.Add(-time.Minute), compatible: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{input: automaticInput(now, automationapp.ModeAutomatic, false, domainscoring.AdviceJoin)}
			repository.input.ScoreRun.WindowEnd = test.windowEnd
			service := newAutomationService(t, repository, now, test.compatible)
			run, err := service.Process(context.Background(), repository.input.ScoreRun.ID, automationapp.TriggerScheduled)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != automationapp.RunFrozen || len(repository.applied) != 0 || len(repository.recorded) != 1 {
				t.Fatalf("unexpected frozen run: %#v", run)
			}
		})
	}
}

func automaticInput(now time.Time, mode automationapp.Mode, current bool, actions ...domainscoring.AdviceAction) automationapp.Input {
	siteID := uuid.New()
	relationID := uuid.New()
	strategyID := uuid.New()
	models := make([]domainautomation.ModelAdvice, len(actions))
	for index, action := range actions {
		models[index] = domainautomation.ModelAdvice{
			Model: string(rune('a' + index)), SnapshotID: uuid.New(), Action: action,
		}
	}
	members := []uuid.UUID(nil)
	if current {
		members = []uuid.UUID{relationID}
	}
	return automationapp.Input{
		ScoreRun: automationapp.ScoreRun{
			ID: uuid.New(), SiteID: siteID, PolicyVersion: domainscoring.PolicyVersionM2ShadowV1,
			WindowEnd: now.Add(-time.Minute), ExpectedTargets: len(actions), CompletedTargets: len(actions), Status: "succeeded",
		},
		Strategies: []automationapp.StrategyInput{{
			ID: strategyID, Kind: string(domainscoring.AutoBalanced), DisplayName: "均衡",
			Enabled: true, Visible: true, Version: 1, Mode: mode, SettingVersion: 1,
			CurrentMemberIDs: members,
			Candidates: []automationapp.Candidate{{
				RelationID: relationID, SupplierID: uuid.New(), SupplierName: "供应商", CurrentMember: current, Models: models,
			}},
		}},
	}
}

func newAutomationService(t *testing.T, repository *fakeRepository, now time.Time, compatible bool) *automationapp.Service {
	t.Helper()
	service, err := automationapp.NewService(
		repository,
		fakeCompatibility{result: automationapp.Compatibility{Ready: compatible, Reasons: []string{"重试配置未通过"}}},
		func() time.Time { return now },
		uuid.New,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func runFromCommand(command automationapp.ApplyCommand) automationapp.Run {
	return automationapp.Run{
		ID: command.RunID, SiteID: command.SiteID, ScoreRunID: command.ScoreRunID,
		Status: command.Status, TriggerKind: command.TriggerKind, Summary: command.Summary,
		StartedAt: command.StartedAt, CompletedAt: command.CompletedAt,
	}
}
