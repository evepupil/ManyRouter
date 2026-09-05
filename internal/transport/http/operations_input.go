package httptransport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var operationRequiredFields = map[string]string{
	"create_site":       "code name new_api_base_url new_api_access_token",
	"update_site":       "name new_api_base_url status version reason",
	"create_supplier":   "code name upstream_base_url upstream_api_key models",
	"update_supplier":   "name upstream_base_url models status version reason",
	"rotate_credential": "version api_key reason",
	"cancel_credential": "version reason",
	"deploy":            "supplier_id sites reason",
	"relation":          "version group_display_name visible desired_status resume reason",
	"strategy":          "version enabled visible display_name member_relation_ids reason",
	"draft_price":       "site_id group_key sale_ratio reason",
	"publish_price":     "version",
	"restore":           "reason",
	"sync":              "",
}

func decodeOperationJSON(c *gin.Context, kind string, target any) bool {
	var raw json.RawMessage
	if !decodeJSON(c, &raw) {
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		writeError(c, http.StatusBadRequest, "invalid_json", "请求内容必须是 JSON 对象")
		return false
	}
	if !requireOperationFields(c, object, operationRequiredFields[kind]) {
		return false
	}
	for _, nested := range []struct{ field, required string }{
		{"models", "model upstream_model input_price output_price currency enabled"},
		{"sites", "site_id group_display_name sale_ratio visible"},
	} {
		if value, ok := object[nested.field]; ok {
			var children []map[string]json.RawMessage
			if err := json.Unmarshal(value, &children); err != nil || children == nil {
				writeError(c, http.StatusBadRequest, "invalid_json", "模型或投放站点必须是对象列表")
				return false
			}
			for _, child := range children {
				if !requireOperationFields(c, child, nested.required) {
					return false
				}
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_json", "请求字段或内容格式无效")
		return false
	}
	return true
}

func requireOperationFields(c *gin.Context, object map[string]json.RawMessage, fields string) bool {
	for _, field := range strings.Fields(fields) {
		value, exists := object[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeError(c, http.StatusBadRequest, "missing_field", "请完整提交所需字段，启用和展示状态需明确选择")
			return false
		}
	}
	return true
}
