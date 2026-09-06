package automation_test

import (
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/automation"
	"github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
)

func TestDecideConservativeSupplierRecordMembership(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		current    bool
		held       bool
		actions    []scoring.AdviceAction
		wantAction automation.Action
		wantMember bool
		wantHold   automation.HoldAction
	}{
		{name: "all models join", actions: []scoring.AdviceAction{scoring.AdviceJoin, scoring.AdviceJoin}, wantAction: automation.ActionJoin, wantMember: true, wantHold: automation.HoldNone},
		{name: "one model still watches", actions: []scoring.AdviceAction{scoring.AdviceJoin, scoring.AdviceWatch}, wantAction: automation.ActionWatch, wantHold: automation.HoldNone},
		{name: "one model exits current member", current: true, actions: []scoring.AdviceAction{scoring.AdviceKeep, scoring.AdviceExit}, wantAction: automation.ActionExit, wantHold: automation.HoldNone},
		{name: "watch keeps current member", current: true, actions: []scoring.AdviceAction{scoring.AdviceKeep, scoring.AdviceWatch}, wantAction: automation.ActionKeep, wantMember: true, wantHold: automation.HoldNone},
		{name: "hard risk applies hold", current: true, actions: []scoring.AdviceAction{scoring.AdviceKeep, scoring.AdviceExclude}, wantAction: automation.ActionExclude, wantHold: automation.HoldApply},
		{name: "held relation waits", held: true, actions: []scoring.AdviceAction{scoring.AdviceJoin, scoring.AdviceWatch}, wantAction: automation.ActionWatch, wantHold: automation.HoldNone},
		{name: "held relation recovers", held: true, actions: []scoring.AdviceAction{scoring.AdviceJoin, scoring.AdviceJoin}, wantAction: automation.ActionRecover, wantMember: true, wantHold: automation.HoldClear},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			models := make([]automation.ModelAdvice, len(test.actions))
			for index, action := range test.actions {
				models[index] = automation.ModelAdvice{Model: string(rune('a' + index)), SnapshotID: uuid.New(), Action: action}
			}
			decision, err := automation.Decide(automation.DecisionInput{CurrentMember: test.current, Held: test.held, Models: models})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != test.wantAction || decision.TargetMember != test.wantMember || decision.HoldAction != test.wantHold {
				t.Fatalf("got action=%s member=%v hold=%s", decision.Action, decision.TargetMember, decision.HoldAction)
			}
		})
	}
}

func TestDecideRejectsDuplicateModelsAndPreservesHardReasons(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	_, err := automation.Decide(automation.DecisionInput{Models: []automation.ModelAdvice{
		{Model: "same", SnapshotID: id, Action: scoring.AdviceJoin},
		{Model: "same", SnapshotID: uuid.New(), Action: scoring.AdviceJoin},
	}})
	if err == nil {
		t.Fatal("duplicate models must be rejected")
	}
	decision, err := automation.Decide(automation.DecisionInput{CurrentMember: true, Models: []automation.ModelAdvice{{
		Model: "model", SnapshotID: id, Action: scoring.AdviceExclude,
		HardReasons: []scoring.GateReason{scoring.GateAuthenticityMismatch},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Reasons) != 1 || decision.Reasons[0] != string(scoring.GateAuthenticityMismatch) {
		t.Fatalf("unexpected reasons: %v", decision.Reasons)
	}
}
