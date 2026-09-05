//go:build acceptance && contract

package compatibility_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/jackc/pgx/v5"
)

func (run *acceptanceRun) prepareGateways() error {
	realBaseURL := strings.TrimRight(run.values["ACCEPTANCE_NEW_API_BASE_URL"], "/")
	realToken := run.values["ACCEPTANCE_NEW_API_ROOT_TOKEN"]
	real, err := newapi.NewClient(realBaseURL, []byte(realToken), run.client)
	if err != nil {
		return err
	}
	run.sites = []acceptanceSite{{BaseURL: realBaseURL, AdminToken: realToken, Client: real}}
	if err := run.recoverPersistedEntryAccess(real); err != nil {
		return err
	}
	run.baseline, err = real.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	rows, err := run.store.Pool().Query(run.ctx, `SELECT c.managed_tag FROM site_supplier_channels c JOIN site_suppliers r ON r.id=c.site_supplier_id JOIN sites s ON s.id=r.site_id WHERE s.code=$1`, run.state.Prefix+"-real")
	if err != nil {
		return err
	}
	tags, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, tag := range tags {
		run.ownedTags[tag] = true
	}
	for _, channel := range run.baseline.Channels {
		if strings.HasPrefix(channel.Name, "m1a-") && !run.ownedTags[channel.ManagedTag] {
			return acceptanceFault{"owned_resource_registry_missing"}
		}
	}
	for _, displayName := range run.baseline.UserUsableGroups {
		if strings.HasPrefix(displayName, "m1a-") && !strings.HasPrefix(displayName, run.state.Prefix) {
			return acceptanceFault{"orphaned_acceptance_group"}
		}
	}
	run.evidence.Counts["real_channels_before"] = len(run.baseline.Channels)
	run.evidence.Versions["real_gateway"] = run.baseline.Version
	localURL := startNewAPI(run.t, run.ctx, os.Getenv("MANYROUTER_NEW_API_BINARY"))
	localToken := initializeAndLogin(run.t, run.ctx, localURL)
	local, err := newapi.NewClient(localURL, []byte(localToken), run.client)
	if err != nil {
		return err
	}
	localState, err := local.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	run.evidence.Versions["local_gateway"] = localState.Version
	target, err := url.Parse(localURL)
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusServiceUnavailable) }
	faultProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if run.failLocal.Load() && strings.HasPrefix(request.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"success":false,"message":"controlled acceptance failure"}`))
			return
		}
		proxy.ServeHTTP(w, request)
	}))
	run.t.Cleanup(faultProxy.Close)
	run.sites = append(run.sites, acceptanceSite{BaseURL: localURL, AdminToken: localToken, Client: local})
	for _, option := range []string{"ModelRatio", "CompletionRatio"} {
		if err := run.remote(1, http.MethodPut, "/api/option/", map[string]any{"key": option, "value": string(mustAcceptanceJSON(map[string]int{run.values["ACCEPTANCE_PUBLIC_MODEL"]: 1}))}, nil); err != nil {
			return err
		}
	}
	run.values["LOCAL_MANAGEMENT_PROXY"] = faultProxy.URL
	run.baselineOptions, err = run.optionDigests()
	return err
}

func mustAcceptanceJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }

func (run *acceptanceRun) recoverPersistedEntryAccess(client *newapi.Client) error {
	if !run.restoreDedicated && !run.restoreAuto {
		return nil
	}
	type group struct {
		Key  string
		Name string
	}
	groups := make([]group, 0, 2)
	if run.restoreDedicated {
		var entry group
		if err := run.store.Pool().QueryRow(run.ctx, `SELECT r.group_key,r.group_display_name FROM site_suppliers r JOIN sites s ON s.id=r.site_id JOIN suppliers p ON p.id=r.supplier_id WHERE s.code=$1 AND p.code=$2`, run.state.Prefix+"-real", run.state.Prefix+"-supplier-1").Scan(&entry.Key, &entry.Name); err != nil {
			return err
		}
		groups = append(groups, entry)
	}
	if run.restoreAuto {
		var entry group
		if err := run.store.Pool().QueryRow(run.ctx, `SELECT st.group_key,st.display_name FROM site_strategies st JOIN sites s ON s.id=st.site_id WHERE s.code=$1 AND st.kind='balanced'`, run.state.Prefix+"-real").Scan(&entry.Key, &entry.Name); err != nil {
			return err
		}
		groups = append(groups, entry)
	}
	actual, err := client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	visible := actual.UserUsableGroups
	changed := false
	for _, entry := range groups {
		var entryChanged bool
		visible, entryChanged = reconciliation.MergeUserUsableGroups(visible, routing.DesiredGroup{Key: entry.Key, DisplayName: entry.Name, Visible: true})
		changed = changed || entryChanged
	}
	if changed {
		if err := client.SetUserUsableGroups(run.ctx, visible); err != nil {
			return err
		}
	}
	confirmed, err := client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	for _, entry := range groups {
		if confirmed.UserUsableGroups[entry.Key] != entry.Name {
			return acceptanceFault{"entry_restoration_unconfirmed"}
		}
	}
	if run.restoreDedicated {
		if err := run.setDedicatedRestoreIntent(false); err != nil {
			return err
		}
	}
	if run.restoreAuto {
		if err := run.setAutoRestoreIntent(false); err != nil {
			return err
		}
	}
	run.evidence.Checks["interrupted_entry_access_recovered"] = true
	return nil
}
