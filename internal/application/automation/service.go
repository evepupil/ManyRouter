package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	domainautomation "github.com/evepupil/ManyRouter/internal/domain/automation"
	"github.com/google/uuid"
)

const maximumScoreAge = 15 * time.Minute

var ErrInvalid = errors.New("invalid automation")

type Service struct {
	repository    Repository
	compatibility CompatibilityChecker
	now           func() time.Time
	newID         func() uuid.UUID
}

func NewService(repository Repository, compatibility CompatibilityChecker, now func() time.Time, newID func() uuid.UUID) (*Service, error) {
	if repository == nil || compatibility == nil || now == nil || newID == nil {
		return nil, errors.New("automation dependencies are required")
	}
	return &Service{repository: repository, compatibility: compatibility, now: now, newID: newID}, nil
}

func (service *Service) ProcessReady(ctx context.Context, trigger TriggerKind) ([]Run, error) {
	if trigger != TriggerScheduled && trigger != TriggerOperator {
		return nil, fmt.Errorf("%w: 自动调整触发方式无效", ErrInvalid)
	}
	runs, err := service.repository.ListReadyScoreRuns(ctx, 20)
	if err != nil {
		return nil, err
	}
	results := make([]Run, 0, len(runs))
	var failures []error
	for _, scoreRun := range runs {
		result, err := service.Process(ctx, scoreRun.ID, trigger)
		if err != nil {
			failures = append(failures, fmt.Errorf("process site %s score run %s: %w", scoreRun.SiteID, scoreRun.ID, err))
			continue
		}
		results = append(results, result)
	}
	return results, errors.Join(failures...)
}

func (service *Service) ProcessLatest(ctx context.Context, siteID uuid.UUID) (Run, error) {
	if siteID == uuid.Nil {
		return Run{}, fmt.Errorf("%w: 站点不能为空", ErrInvalid)
	}
	run, err := service.repository.GetLatestSuccessfulScoreRun(ctx, siteID)
	if err != nil {
		return Run{}, err
	}
	return service.Process(ctx, run.ID, TriggerOperator)
}

func (service *Service) Process(ctx context.Context, scoreRunID uuid.UUID, trigger TriggerKind) (Run, error) {
	if scoreRunID == uuid.Nil || (trigger != TriggerScheduled && trigger != TriggerOperator) {
		return Run{}, fmt.Errorf("%w: 自动调整请求无效", ErrInvalid)
	}
	input, err := service.repository.LoadAutomationInput(ctx, scoreRunID)
	if err != nil {
		return Run{}, err
	}
	now := service.now().UTC()
	command := ApplyCommand{
		RunID: service.newID(), SiteID: input.ScoreRun.SiteID, ScoreRunID: input.ScoreRun.ID,
		TriggerKind: trigger, StartedAt: now, CompletedAt: now,
	}
	if reason := frozenReason(input, now); reason != "" {
		command.Status = RunFrozen
		command.Summary = reason
		return service.repository.RecordAutomationRun(ctx, command)
	}
	automatic := false
	for _, strategy := range input.Strategies {
		if strategy.Mode == ModeAutomatic && strategy.Enabled {
			automatic = true
			break
		}
	}
	if automatic {
		compatibility, checkErr := service.compatibility.CheckAutomationCompatibility(ctx, input.ScoreRun.SiteID)
		if checkErr != nil {
			return Run{}, checkErr
		}
		if !compatibility.Ready {
			command.Status = RunFrozen
			command.Summary = "New API 故障切换配置未通过：" + strings.Join(compatibility.Reasons, "；")
			return service.repository.RecordAutomationRun(ctx, command)
		}
	}
	changed := false
	previewed := false
	for _, strategy := range input.Strategies {
		if !strategy.Enabled || strategy.Mode == ModeManual && trigger == TriggerScheduled {
			continue
		}
		decisions, update, strategyChanged, decideErr := service.decideStrategy(strategy, now)
		if decideErr != nil {
			return Run{}, decideErr
		}
		command.Decisions = append(command.Decisions, decisions...)
		if strategy.Mode == ModeManual {
			previewed = true
			continue
		}
		if strategyChanged {
			command.Strategies = append(command.Strategies, update)
			changed = true
		}
		for _, decision := range decisions {
			if decision.HoldAction != domainautomation.HoldNone {
				changed = true
			}
		}
	}
	if !changed {
		if previewed {
			command.Status = RunPreview
			command.Summary = "已生成自动调整预览，人工维护模式未修改线路"
			return service.repository.RecordAutomationRun(ctx, command)
		}
		command.Status = RunNoChange
		command.Summary = "完整评分批次未产生成员变化"
		return service.repository.RecordAutomationRun(ctx, command)
	}
	command.Status = RunPendingSync
	command.Summary = fmt.Sprintf("生成 %d 项自动决定，等待线路核对", len(command.Decisions))
	return service.repository.ApplyAutomationRun(ctx, command)
}

func (service *Service) decideStrategy(strategy StrategyInput, now time.Time) ([]Decision, StrategyUpdate, bool, error) {
	members := make(map[uuid.UUID]bool, len(strategy.CurrentMemberIDs))
	for _, id := range strategy.CurrentMemberIDs {
		members[id] = true
	}
	decisions := make([]Decision, 0, len(strategy.Candidates))
	configurationChanged := false
	for _, candidate := range strategy.Candidates {
		decision, err := domainautomation.Decide(domainautomation.DecisionInput{
			CurrentMember: members[candidate.RelationID], Held: candidate.Held, Models: candidate.Models,
		})
		if err != nil {
			return nil, StrategyUpdate{}, false, fmt.Errorf("decide %s/%s: %w", strategy.Kind, candidate.RelationID, err)
		}
		if decision.TargetMember {
			members[candidate.RelationID] = true
		} else {
			delete(members, candidate.RelationID)
		}
		if decision.TargetMember != candidate.CurrentMember {
			configurationChanged = true
		}
		decisions = append(decisions, Decision{
			ID: service.newID(), StrategyID: strategy.ID, StrategyKind: strategy.Kind,
			RelationID: candidate.RelationID, SupplierName: candidate.SupplierName,
			Action: decision.Action, CurrentMember: candidate.CurrentMember, TargetMember: decision.TargetMember,
			HoldAction: decision.HoldAction, Reasons: decision.Reasons, SnapshotIDs: decision.SnapshotIDs, CreatedAt: now,
		})
	}
	memberIDs := make([]uuid.UUID, 0, len(members))
	for id := range members {
		memberIDs = append(memberIDs, id)
	}
	sort.Slice(memberIDs, func(i, j int) bool { return memberIDs[i].String() < memberIDs[j].String() })
	visible := strategy.Visible
	automaticallyClosed := strategy.EntryClosedByAutomation
	if len(memberIDs) == 0 && strategy.Visible {
		visible = false
		automaticallyClosed = true
		configurationChanged = true
	} else if len(memberIDs) > 0 && strategy.EntryClosedByAutomation {
		visible = true
		automaticallyClosed = false
		configurationChanged = true
	}
	return decisions, StrategyUpdate{
		StrategyID: strategy.ID, ExpectedStrategyVersion: strategy.Version,
		ExpectedSettingVersion: strategy.SettingVersion, MemberRelationIDs: memberIDs,
		Visible: visible, EntryClosedByAutomation: automaticallyClosed,
	}, configurationChanged || !sameUUIDSet(strategy.CurrentMemberIDs, memberIDs), nil
}

func sameUUIDSet(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[uuid.UUID]bool, len(left))
	for _, id := range left {
		values[id] = true
	}
	for _, id := range right {
		if !values[id] {
			return false
		}
	}
	return true
}

func frozenReason(input Input, now time.Time) string {
	run := input.ScoreRun
	if run.Status != "succeeded" || run.ExpectedTargets < 1 || run.CompletedTargets != run.ExpectedTargets {
		return "评分批次不完整，自动调整已冻结"
	}
	if run.WindowEnd.IsZero() || now.Before(run.WindowEnd) || now.Sub(run.WindowEnd) > maximumScoreAge {
		return "评分数据已经过期，自动调整已冻结"
	}
	return ""
}

func (service *Service) ListSettings(ctx context.Context, siteID uuid.UUID) ([]Setting, error) {
	if siteID == uuid.Nil {
		return nil, fmt.Errorf("%w: 站点不能为空", ErrInvalid)
	}
	return service.repository.ListAutomationSettings(ctx, siteID)
}

func (service *Service) UpdateSetting(ctx context.Context, command UpdateSettingCommand) (Setting, error) {
	if command.SiteID == uuid.Nil || strings.TrimSpace(command.StrategyKind) == "" ||
		(command.Mode != ModeManual && command.Mode != ModeAutomatic) || command.Version < 0 ||
		strings.TrimSpace(command.Actor) == "" || len(strings.TrimSpace(command.Reason)) < 3 || len(strings.TrimSpace(command.Reason)) > 500 {
		return Setting{}, fmt.Errorf("%w: 自动调整设置无效", ErrInvalid)
	}
	if command.Mode == ModeAutomatic {
		result, err := service.compatibility.CheckAutomationCompatibility(ctx, command.SiteID)
		if err != nil {
			return Setting{}, err
		}
		if !result.Ready {
			return Setting{}, fmt.Errorf("%w: New API 故障切换配置未通过：%s", ErrInvalid, strings.Join(result.Reasons, "；"))
		}
	}
	command.Reason = strings.TrimSpace(command.Reason)
	command.Actor = strings.TrimSpace(command.Actor)
	return service.repository.UpdateAutomationSetting(ctx, command, service.now().UTC())
}

func (service *Service) ListRuns(ctx context.Context, filter RunFilter) (RunPage, error) {
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return RunPage{}, fmt.Errorf("%w: 自动调整分页参数无效", ErrInvalid)
	}
	return service.repository.ListAutomationRuns(ctx, filter)
}
