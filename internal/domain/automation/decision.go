package automation

import (
	"errors"
	"sort"
	"strings"

	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
)

type Action string

const (
	ActionJoin    Action = "join"
	ActionKeep    Action = "keep"
	ActionExit    Action = "exit"
	ActionExclude Action = "exclude"
	ActionWatch   Action = "watch"
	ActionRecover Action = "recover"
)

type HoldAction string

const (
	HoldNone  HoldAction = "none"
	HoldApply HoldAction = "apply"
	HoldClear HoldAction = "clear"
)

type ModelAdvice struct {
	Model       string
	SnapshotID  uuid.UUID
	Action      domainscoring.AdviceAction
	Reasons     []string
	HardReasons []domainscoring.GateReason
}

type DecisionInput struct {
	CurrentMember bool
	Held          bool
	Models        []ModelAdvice
}

type Decision struct {
	Action       Action
	TargetMember bool
	HoldAction   HoldAction
	Reasons      []string
	SnapshotIDs  []uuid.UUID
}

func Decide(input DecisionInput) (Decision, error) {
	if len(input.Models) == 0 {
		return Decision{}, errors.New("automation decision requires model advice")
	}
	seenModels := make(map[string]bool, len(input.Models))
	reasons := make(map[string]bool)
	snapshotIDs := make([]uuid.UUID, 0, len(input.Models))
	allJoin := true
	anyExit := false
	anyExclude := false
	for _, advice := range input.Models {
		model := strings.TrimSpace(advice.Model)
		if model == "" || advice.SnapshotID == uuid.Nil || seenModels[model] {
			return Decision{}, errors.New("automation decision contains invalid model advice")
		}
		seenModels[model] = true
		snapshotIDs = append(snapshotIDs, advice.SnapshotID)
		for _, reason := range advice.Reasons {
			if normalized := strings.TrimSpace(reason); normalized != "" {
				reasons[normalized] = true
			}
		}
		for _, reason := range advice.HardReasons {
			if reason != "" {
				reasons[string(reason)] = true
			}
		}
		switch advice.Action {
		case domainscoring.AdviceJoin:
		case domainscoring.AdviceExit:
			allJoin = false
			anyExit = true
		case domainscoring.AdviceExclude:
			allJoin = false
			anyExclude = true
		case domainscoring.AdviceKeep, domainscoring.AdviceWatch:
			allJoin = false
		default:
			return Decision{}, errors.New("automation decision contains unsupported advice")
		}
	}
	sort.Slice(snapshotIDs, func(i, j int) bool { return snapshotIDs[i].String() < snapshotIDs[j].String() })
	result := Decision{Action: ActionWatch, TargetMember: input.CurrentMember, HoldAction: HoldNone, Reasons: sortedReasons(reasons), SnapshotIDs: snapshotIDs}
	if anyExclude {
		if len(result.Reasons) == 0 {
			result.Reasons = []string{"hard_gate_failed"}
		}
		result.Action = ActionExclude
		result.TargetMember = false
		if !input.Held {
			result.HoldAction = HoldApply
		}
		return result, nil
	}
	if input.Held {
		result.TargetMember = false
		if allJoin {
			result.Action = ActionRecover
			result.TargetMember = true
			result.HoldAction = HoldClear
		}
		return result, nil
	}
	if input.CurrentMember {
		result.Action = ActionKeep
		result.TargetMember = true
		if anyExit {
			result.Action = ActionExit
			result.TargetMember = false
		}
		return result, nil
	}
	result.TargetMember = false
	if allJoin {
		result.Action = ActionJoin
		result.TargetMember = true
	}
	return result, nil
}

func sortedReasons(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
