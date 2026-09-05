//go:build acceptance && contract

package compatibility_test

import (
	"context"
	"net/http"
	"net/url"
	"time"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

func (run *acceptanceRun) setDedicatedRestoreIntent(required bool) error {
	previous := run.state.RestoreDedicated
	run.state.RestoreDedicated = required
	if err := run.saveState(); err != nil {
		run.state.RestoreDedicated = previous
		return acceptanceFault{"restore_intent_persistence_failed"}
	}
	run.restoreDedicated = required
	return nil
}

func (run *acceptanceRun) setAutoRestoreIntent(required bool) error {
	previous := run.state.RestoreAuto
	run.state.RestoreAuto = required
	if err := run.saveState(); err != nil {
		run.state.RestoreAuto = previous
		return acceptanceFault{"restore_intent_persistence_failed"}
	}
	run.restoreAuto = required
	return nil
}

func (run *acceptanceRun) confirmEntryAccessRecovered() error {
	if !run.restoreDedicated && !run.restoreAuto {
		return nil
	}
	if len(run.sites) == 0 || len(run.sites[0].Relations) == 0 {
		return acceptanceFault{"entry_restoration_unavailable"}
	}
	actual, err := run.sites[0].Client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	if run.restoreDedicated {
		if _, open := actual.UserUsableGroups[run.sites[0].Relations[0].GroupKey]; !open {
			return acceptanceFault{"dedicated_entry_restoration_unconfirmed"}
		}
		if err := run.setDedicatedRestoreIntent(false); err != nil {
			return err
		}
	}
	if run.restoreAuto {
		if _, open := actual.UserUsableGroups[run.sites[0].AutoGroup]; !open {
			return acceptanceFault{"auto_entry_restoration_unconfirmed"}
		}
		if err := run.setAutoRestoreIntent(false); err != nil {
			return err
		}
	}
	run.evidence.Checks["interrupted_entry_access_recovered"] = true
	return nil
}

func (run *acceptanceRun) restoreEntryAccess() error {
	if !run.restoreDedicated && !run.restoreAuto {
		return nil
	}
	if run.api == nil || run.workerStopped || len(run.sites) == 0 {
		return acceptanceFault{"entry_restoration_unavailable"}
	}
	previous := run.ctx
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	run.ctx = cleanup
	defer func() { run.ctx = previous }()
	if run.restoreDedicated {
		if err := run.setDedicatedEntry(0, true); err != nil {
			return err
		}
		if run.sites[0].DedicatedKey != "" {
			if err := run.callModel(0, run.sites[0].DedicatedKey, http.StatusOK); err != nil {
				return err
			}
		}
		if err := run.setDedicatedRestoreIntent(false); err != nil {
			return err
		}
	}
	if run.restoreAuto {
		strategy, err := run.strategy(0)
		if err != nil {
			return err
		}
		if err := run.saveStrategy(0, strategy.Members, true, true); err != nil {
			return err
		}
		if run.sites[0].AutoKey != "" {
			if err := run.callModel(0, run.sites[0].AutoKey, http.StatusOK); err != nil {
				return err
			}
		}
		if err := run.setAutoRestoreIntent(false); err != nil {
			return err
		}
	}
	run.evidence.Checks["entry_access_restored_after_failure"] = true
	return nil
}

func (run *acceptanceRun) pauseTemporarySite() error {
	if run.api == nil || len(run.sites) < 2 || run.sites[1].ID == uuid.Nil {
		return nil
	}
	var page acceptancePage[acceptanceSiteRecord]
	if err := run.apiRequest(http.MethodGet, "/sites?q="+url.QueryEscape(run.state.Prefix+"-local-")+"&limit=100", nil, &page); err != nil {
		return err
	}
	for _, site := range page.Items {
		if site.ID != run.sites[1].ID {
			continue
		}
		if site.Status == "disabled" {
			return nil
		}
		input := domain.SiteInput{Name: site.Name, NewAPIBaseURL: site.BaseURL, AdminUserID: site.AdminUserID, Status: "disabled", Version: site.Version, Reason: "M1 acceptance ends the temporary local site"}
		if err := run.apiRequest(http.MethodPut, "/sites/"+site.ID.String(), input, nil); err != nil {
			return err
		}
		run.evidence.Checks["temporary_site_paused"] = true
		return nil
	}
	return acceptanceFault{"temporary_site_record_missing"}
}
