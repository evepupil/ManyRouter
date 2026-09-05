//go:build acceptance && contract

package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	acceptanceRemoteResponseLimit = 2 << 20
	acceptanceTokenPageSize       = 100
	acceptanceTokenPageLimit      = 100
)

type acceptanceRemoteEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type acceptanceToken struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Group      string   `json:"group"`
	AutoGroups []string `json:"auto_groups"`
}

type acceptanceTokenPage struct {
	Items []acceptanceToken `json:"items"`
	Total int               `json:"total"`
}

func (run *acceptanceRun) remote(slot int, method, path string, body, target any) error {
	return run.remoteContext(run.ctx, slot, method, path, body, target)
}

func (run *acceptanceRun) remoteContext(ctx context.Context, slot int, method, path string, body, target any) error {
	if slot < 0 || slot >= len(run.sites) || !strings.HasPrefix(path, "/") {
		return acceptanceFault{"new_api_request_invalid"}
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return acceptanceFault{"new_api_request_invalid"}
		}
		defer clear(payload)
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, run.sites[slot].BaseURL+path, reader)
	if err != nil {
		return acceptanceFault{"new_api_request_invalid"}
	}
	request.Header.Set("Authorization", "Bearer "+run.sites[slot].AdminToken)
	request.Header.Set("New-Api-User", "1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := run.client.Do(request)
	if err != nil {
		return acceptanceFault{"new_api_request_failed"}
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, acceptanceRemoteResponseLimit+1))
	if err != nil {
		return acceptanceFault{"new_api_response_unreadable"}
	}
	defer clear(payload)
	if len(payload) > acceptanceRemoteResponseLimit {
		return acceptanceFault{"new_api_response_too_large"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return acceptanceFault{"new_api_http_rejected"}
	}
	var envelope acceptanceRemoteEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return acceptanceFault{"new_api_response_invalid"}
	}
	if !envelope.Success {
		return acceptanceFault{"new_api_operation_rejected"}
	}
	if target != nil {
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return acceptanceFault{"new_api_response_incomplete"}
		}
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return acceptanceFault{"new_api_response_invalid"}
		}
	}
	return nil
}

func (run *acceptanceRun) listTokens(ctx context.Context, slot int) ([]acceptanceToken, error) {
	tokens := make([]acceptanceToken, 0)
	for page := 1; page <= acceptanceTokenPageLimit; page++ {
		var result acceptanceTokenPage
		path := "/api/token/?p=" + strconv.Itoa(page) + "&size=" + strconv.Itoa(acceptanceTokenPageSize)
		if err := run.remoteContext(ctx, slot, http.MethodGet, path, nil, &result); err != nil {
			return nil, err
		}
		tokens = append(tokens, result.Items...)
		if len(result.Items) == 0 || (result.Total > 0 && len(tokens) >= result.Total) {
			return tokens, nil
		}
		if result.Total == 0 && len(result.Items) < acceptanceTokenPageSize {
			return tokens, nil
		}
	}
	return nil, acceptanceFault{"new_api_token_page_limit"}
}

func exactAcceptanceToken(tokens []acceptanceToken, name string) (*acceptanceToken, error) {
	var matched *acceptanceToken
	for index := range tokens {
		if tokens[index].Name != name {
			continue
		}
		if matched != nil {
			return nil, acceptanceFault{"temporary_key_name_ambiguous"}
		}
		matched = &tokens[index]
	}
	return matched, nil
}

func resolveCleanupToken(tokens []acceptanceToken, entry acceptanceTempKey) (*acceptanceToken, bool, error) {
	byName, err := exactAcceptanceToken(tokens, entry.Name)
	if err != nil {
		return nil, false, err
	}
	var byID *acceptanceToken
	if entry.ID > 0 {
		for index := range tokens {
			if tokens[index].ID != entry.ID {
				continue
			}
			if byID != nil {
				return nil, false, acceptanceFault{"temporary_key_id_ambiguous"}
			}
			byID = &tokens[index]
		}
	}
	if byID != nil {
		if byID.Name != entry.Name || byName == nil || byName.ID != entry.ID {
			return nil, false, acceptanceFault{"temporary_key_identity_changed"}
		}
		return byID, false, nil
	}
	if byName != nil {
		if entry.ID > 0 {
			return nil, false, acceptanceFault{"temporary_key_identity_changed"}
		}
		return byName, false, nil
	}
	if entry.ID == 0 && entry.Site == 0 {
		return nil, false, acceptanceFault{"temporary_key_result_unknown"}
	}
	return nil, true, nil
}

func (run *acceptanceRun) createUserKey(slot int, purpose, group string) (string, error) {
	if slot < 0 || slot >= len(run.sites) || group == "" {
		return "", acceptanceFault{"temporary_key_configuration_invalid"}
	}
	name := run.state.Prefix + "-s" + strconv.Itoa(slot+1) + "-" + purpose + "-" + uuid.NewString()[:8]
	entry := &acceptanceTempKey{Site: slot, Name: name, Uncertain: true}
	if _, err := run.store.Pool().Exec(run.ctx, `INSERT INTO acceptance_temp_keys(site_slot,token_name,token_id,deleted) VALUES($1,$2,NULL,false)`, slot, name); err != nil {
		return "", acceptanceFault{"temporary_key_registration_failed"}
	}
	run.keys = append(run.keys, entry)
	run.evidence.Checks["temporary_keys_removed"] = false

	request := map[string]any{
		"name":                 name,
		"expired_time":         -1,
		"remain_quota":         0,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                group,
	}
	if err := run.remote(slot, http.MethodPost, "/api/token/", request, nil); err != nil {
		return "", err
	}
	tokens, err := run.listTokens(run.ctx, slot)
	if err != nil {
		return "", err
	}
	created, err := exactAcceptanceToken(tokens, name)
	if err != nil {
		return "", err
	}
	if created == nil || created.ID < 1 {
		return "", acceptanceFault{"temporary_key_not_found"}
	}
	var exact acceptanceToken
	path := "/api/token/" + strconv.FormatInt(created.ID, 10)
	if err := run.remote(slot, http.MethodGet, path, nil, &exact); err != nil {
		return "", err
	}
	if exact.ID != created.ID || exact.Name != name || exact.Group != group || len(exact.AutoGroups) != 0 {
		return "", acceptanceFault{"temporary_key_verification_failed"}
	}
	var keyResult struct {
		Key string `json:"key"`
	}
	if err := run.remote(slot, http.MethodPost, path+"/key", map[string]any{}, &keyResult); err != nil {
		return "", err
	}
	if keyResult.Key == "" {
		return "", acceptanceFault{"temporary_key_unavailable"}
	}
	command, err := run.store.Pool().Exec(run.ctx, `UPDATE acceptance_temp_keys SET token_id=$3 WHERE site_slot=$1 AND token_name=$2 AND deleted=false`, slot, name, created.ID)
	if err != nil || command.RowsAffected() != 1 {
		return "", acceptanceFault{"temporary_key_registration_failed"}
	}
	entry.ID = created.ID
	entry.Key = keyResult.Key
	entry.Uncertain = false
	return keyResult.Key, nil
}

func (run *acceptanceRun) readUsedQuota(ctx context.Context, slot int) (int64, error) {
	var profile struct {
		UsedQuota *int64 `json:"used_quota"`
	}
	if err := run.remoteContext(ctx, slot, http.MethodGet, "/api/user/self", nil, &profile); err != nil {
		return 0, err
	}
	if profile.UsedQuota == nil {
		return 0, acceptanceFault{"usage_counter_unavailable"}
	}
	return *profile.UsedQuota, nil
}

func (run *acceptanceRun) waitForUsageIncrease(slot int, before int64) error {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := run.readUsedQuota(run.ctx, slot)
		if err != nil {
			return err
		}
		if current > before {
			return nil
		}
		select {
		case <-run.ctx.Done():
			return acceptanceFault{"usage_counter_timeout"}
		case <-deadline.C:
			return acceptanceFault{"usage_counter_unchanged"}
		case <-ticker.C:
		}
	}
}

func (run *acceptanceRun) callModel(slot int, key string, wantStatus int) error {
	if slot < 0 || slot >= len(run.sites) || key == "" {
		return acceptanceFault{"model_call_configuration_invalid"}
	}
	payload, err := json.Marshal(map[string]any{
		"model":      run.values["ACCEPTANCE_PUBLIC_MODEL"],
		"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
		"max_tokens": 1,
	})
	if err != nil {
		return acceptanceFault{"model_call_configuration_invalid"}
	}
	defer clear(payload)
	callContext, cancel := context.WithTimeout(run.ctx, 2*time.Minute)
	defer cancel()
	for {
		request, err := http.NewRequestWithContext(callContext, http.MethodPost, run.sites[slot].BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return acceptanceFault{"model_call_configuration_invalid"}
		}
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", "application/json")
		response, err := run.client.Do(request)
		if err != nil {
			return acceptanceFault{"model_call_failed"}
		}
		if response.StatusCode == http.StatusTooManyRequests {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			run.evidence.Counts["model_call_429_retries"]++
			select {
			case <-callContext.Done():
				return acceptanceFault{"model_call_rate_limit_timeout"}
			case <-time.After(5 * time.Second):
				continue
			}
		}
		if response.StatusCode != wantStatus {
			run.evidence.Counts["model_call_unexpected_status"] = response.StatusCode
			_ = response.Body.Close()
			return acceptanceFault{"model_call_status_mismatch"}
		}
		if wantStatus != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			return nil
		}
		var shape struct {
			ID      string `json:"id"`
			Choices []struct {
				Index int `json:"index"`
			} `json:"choices"`
		}
		decoder := json.NewDecoder(io.LimitReader(response.Body, acceptanceRemoteResponseLimit+1))
		decodeErr := decoder.Decode(&shape)
		_ = response.Body.Close()
		if decodeErr != nil {
			return acceptanceFault{"model_call_response_invalid"}
		}
		if shape.ID == "" || len(shape.Choices) == 0 {
			return acceptanceFault{"model_call_shape_invalid"}
		}
		return nil
	}
}

func (run *acceptanceRun) exerciseUserRequests() error {
	if err := run.cleanupKeys(); err != nil {
		return err
	}
	realTokens, err := run.listTokens(run.ctx, 0)
	if err != nil {
		return err
	}
	run.realKeyBaseline = make(map[int64]struct{}, len(realTokens))
	for _, token := range realTokens {
		run.realKeyBaseline[token.ID] = struct{}{}
	}
	run.evidence.Counts["real_user_keys_before"] = len(realTokens)
	localTokens, err := run.listTokens(run.ctx, 1)
	if err != nil {
		return err
	}
	run.evidence.Counts["local_user_keys_before"] = len(localTokens)

	for slot := range run.sites {
		if len(run.sites[slot].Relations) == 0 || run.sites[slot].AutoGroup == "" {
			return acceptanceFault{"temporary_key_configuration_invalid"}
		}
		before, err := run.readUsedQuota(run.ctx, slot)
		if err != nil {
			return err
		}
		dedicated, err := run.createUserKey(slot, "ded", run.sites[slot].Relations[0].GroupKey)
		if err != nil {
			return err
		}
		automatic, err := run.createUserKey(slot, "auto", run.sites[slot].AutoGroup)
		if err != nil {
			return err
		}
		run.sites[slot].DedicatedKey = dedicated
		run.sites[slot].AutoKey = automatic
		if err := run.callModel(slot, dedicated, http.StatusOK); err != nil {
			return err
		}
		if err := run.callModel(slot, automatic, http.StatusOK); err != nil {
			return err
		}
		if err := run.waitForUsageIncrease(slot, before); err != nil {
			return err
		}
		if slot == 0 {
			run.evidence.Checks["real_usage_increased"] = true
		} else {
			run.evidence.Checks["local_usage_increased"] = true
		}
	}
	run.evidence.Counts["temporary_keys_created"] = len(run.keys)
	run.evidence.Checks["dedicated_and_auto_keys_called"] = true
	return nil
}

func (run *acceptanceRun) exerciseStoppedBackend() error {
	for slot := range run.sites {
		if err := run.callModel(slot, run.sites[slot].AutoKey, http.StatusOK); err != nil {
			return err
		}
	}
	run.evidence.Checks["gateways_work_without_manyrouter_backend"] = true
	return nil
}

func (run *acceptanceRun) cleanupKeys() error {
	run.evidence.Checks["temporary_keys_removed"] = false
	if run.store == nil {
		if len(run.keys) == 0 {
			run.evidence.Checks["temporary_keys_removed"] = true
			return nil
		}
		run.cleanupErrors = append(run.cleanupErrors, errors.New("temporary key registry unavailable"))
		return acceptanceFault{"temporary_key_cleanup_failed"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	byIdentity := make(map[string]*acceptanceTempKey)
	for _, entry := range run.keys {
		byIdentity[strconv.Itoa(entry.Site)+"\x00"+entry.Name] = entry
	}
	rows, err := run.store.Pool().Query(ctx, `SELECT site_slot,token_name,COALESCE(token_id,0),deleted FROM acceptance_temp_keys WHERE deleted=false ORDER BY site_slot,token_name`)
	if err != nil {
		run.cleanupErrors = append(run.cleanupErrors, err)
	} else {
		for rows.Next() {
			entry := &acceptanceTempKey{Uncertain: true}
			if scanErr := rows.Scan(&entry.Site, &entry.Name, &entry.ID, &entry.Deleted); scanErr != nil {
				run.cleanupErrors = append(run.cleanupErrors, scanErr)
				continue
			}
			identity := strconv.Itoa(entry.Site) + "\x00" + entry.Name
			if current, exists := byIdentity[identity]; exists {
				if current.ID == 0 {
					current.ID = entry.ID
				}
				continue
			}
			byIdentity[identity] = entry
			run.keys = append(run.keys, entry)
		}
		if rows.Err() != nil {
			run.cleanupErrors = append(run.cleanupErrors, rows.Err())
		}
		rows.Close()
	}

	removed := 0
	for _, entry := range byIdentity {
		if entry.Deleted {
			continue
		}
		if entry.Site < 0 || entry.Site >= len(run.sites) {
			run.cleanupErrors = append(run.cleanupErrors, errors.New("temporary key site unavailable"))
			continue
		}
		tokens, listErr := run.listTokens(ctx, entry.Site)
		if listErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, listErr)
			continue
		}
		matched, missing, matchErr := resolveCleanupToken(tokens, *entry)
		if matchErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, matchErr)
			continue
		}
		if missing {
			if markErr := run.markKeyDeleted(ctx, entry, 0); markErr != nil {
				run.cleanupErrors = append(run.cleanupErrors, markErr)
				continue
			}
			entry.Deleted = true
			entry.Key = ""
			continue
		}
		var exact acceptanceToken
		path := "/api/token/" + strconv.FormatInt(matched.ID, 10)
		if getErr := run.remoteContext(ctx, entry.Site, http.MethodGet, path, nil, &exact); getErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, getErr)
			continue
		}
		if exact.ID != matched.ID || exact.Name != entry.Name {
			run.cleanupErrors = append(run.cleanupErrors, errors.New("temporary key ownership check failed"))
			continue
		}
		if deleteErr := run.remoteContext(ctx, entry.Site, http.MethodDelete, path, nil, nil); deleteErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, deleteErr)
			continue
		}
		remaining, listErr := run.listTokens(ctx, entry.Site)
		if listErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, listErr)
			continue
		}
		stillPresent := false
		for _, token := range remaining {
			if token.ID == matched.ID || token.Name == entry.Name {
				stillPresent = true
				break
			}
		}
		if stillPresent {
			run.cleanupErrors = append(run.cleanupErrors, errors.New("temporary key remained after deletion"))
			continue
		}
		if markErr := run.markKeyDeleted(ctx, entry, matched.ID); markErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, markErr)
			continue
		}
		entry.ID = matched.ID
		entry.Deleted = true
		entry.Uncertain = false
		entry.Key = ""
		removed++
	}

	if run.realKeyBaseline != nil && len(run.sites) > 0 {
		current, listErr := run.listTokens(ctx, 0)
		if listErr != nil {
			run.cleanupErrors = append(run.cleanupErrors, listErr)
		} else {
			run.evidence.Counts["real_user_keys_after"] = len(current)
			if len(current) != len(run.realKeyBaseline) {
				run.cleanupErrors = append(run.cleanupErrors, errors.New("real key baseline count changed"))
			} else {
				for _, token := range current {
					if _, exists := run.realKeyBaseline[token.ID]; !exists {
						run.cleanupErrors = append(run.cleanupErrors, errors.New("real key baseline identity changed"))
						break
					}
				}
			}
		}
	}
	run.evidence.Counts["temporary_keys_removed"] = removed
	if len(run.cleanupErrors) != 0 {
		return acceptanceFault{"temporary_key_cleanup_failed"}
	}
	run.evidence.Checks["temporary_keys_removed"] = true
	return nil
}

func (run *acceptanceRun) markKeyDeleted(ctx context.Context, entry *acceptanceTempKey, tokenID int64) error {
	command, err := run.store.Pool().Exec(ctx, `UPDATE acceptance_temp_keys SET token_id=COALESCE(token_id,NULLIF($3,0)),deleted=true WHERE site_slot=$1 AND token_name=$2 AND deleted=false`, entry.Site, entry.Name, tokenID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("temporary key registry row missing")
	}
	return nil
}

func TestResolveCleanupTokenUsesRegisteredIDAndUniqueName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		tokens      []acceptanceToken
		entry       acceptanceTempKey
		wantID      int64
		wantMissing bool
		wantError   string
	}{
		{name: "registered identity", tokens: []acceptanceToken{{ID: 11, Name: "temporary"}}, entry: acceptanceTempKey{Site: 0, ID: 11, Name: "temporary"}, wantID: 11},
		{name: "registered ID renamed", tokens: []acceptanceToken{{ID: 11, Name: "renamed"}}, entry: acceptanceTempKey{Site: 0, ID: 11, Name: "temporary"}, wantError: "temporary_key_identity_changed"},
		{name: "registered name reused", tokens: []acceptanceToken{{ID: 12, Name: "temporary"}}, entry: acceptanceTempKey{Site: 0, ID: 11, Name: "temporary"}, wantError: "temporary_key_identity_changed"},
		{name: "registered identity absent", entry: acceptanceTempKey{Site: 0, ID: 11, Name: "temporary"}, wantMissing: true},
		{name: "real result unknown", entry: acceptanceTempKey{Site: 0, Name: "temporary"}, wantError: "temporary_key_result_unknown"},
		{name: "local result absent", entry: acceptanceTempKey{Site: 1, Name: "temporary"}, wantMissing: true},
		{name: "uncertain identity found by name", tokens: []acceptanceToken{{ID: 13, Name: "temporary"}}, entry: acceptanceTempKey{Site: 0, Name: "temporary"}, wantID: 13},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			token, missing, err := resolveCleanupToken(test.tokens, test.entry)
			if test.wantError != "" {
				if err == nil || acceptanceErrorCode(err) != test.wantError {
					t.Fatalf("unexpected cleanup decision error: %v", err)
				}
				if token != nil || missing {
					t.Fatal("conflicting cleanup identity was treated as removable")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if missing != test.wantMissing {
				t.Fatalf("missing decision: got %t want %t", missing, test.wantMissing)
			}
			if test.wantID == 0 {
				if token != nil {
					t.Fatalf("unexpected cleanup token: %#v", token)
				}
				return
			}
			if token == nil || token.ID != test.wantID {
				t.Fatalf("cleanup token: %#v", token)
			}
		})
	}
}
