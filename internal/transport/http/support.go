package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"

	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (h *Handler) lookupIdempotency(c *gin.Context, scope, key string, request any) (*idempotency.Record, string, bool) {
	record, hash, err := h.idempotency.Lookup(c.Request.Context(), scope, key, request)
	if err != nil {
		if errors.Is(err, idempotency.ErrKeyReused) {
			writeError(c, stdhttp.StatusConflict, "idempotency_key_reused", "该幂等键已经用于另一份请求")
		} else if strings.Contains(err.Error(), "idempotency key") {
			writeError(c, stdhttp.StatusBadRequest, "invalid_idempotency_key", "幂等键格式无效")
		} else {
			h.writeApplicationError(c, err)
		}
		return nil, "", false
	}
	return record, hash, true
}

func (h *Handler) replay(c *gin.Context, record *idempotency.Record) bool {
	if record == nil {
		return false
	}
	c.Data(record.StatusCode, "application/json; charset=utf-8", record.Response)
	return true
}

func (h *Handler) writeIdempotent(c *gin.Context, scope, key, requestHash string, status int, response any) {
	if err := h.idempotency.Save(c.Request.Context(), scope, key, requestHash, status, response); err != nil {
		h.writeApplicationError(c, err)
		return
	}
	c.JSON(status, response)
}

func (h *Handler) writeApplicationError(c *gin.Context, err error) {
	if errors.Is(err, evaluationapp.ErrInvalid) {
		writeError(c, stdhttp.StatusBadRequest, "invalid_evaluation", strings.TrimPrefix(err.Error(), evaluationapp.ErrInvalid.Error()+": "))
		return
	}
	if errors.Is(err, evaluationapp.ErrDailyBudgetExceeded) {
		writeError(c, stdhttp.StatusConflict, "evaluation_budget_exhausted", "该供应商模型今天的主动测评预算已经用完")
		return
	}
	if errors.Is(err, evaluationapp.ErrRequestKeyReused) {
		writeError(c, stdhttp.StatusConflict, "idempotency_key_reused", "该幂等键已经用于另一份请求")
		return
	}
	if errors.Is(err, operations.ErrInvalid) {
		writeError(c, stdhttp.StatusBadRequest, "invalid_request", strings.TrimPrefix(err.Error(), operations.ErrInvalid.Error()+": "))
		return
	}
	if errors.Is(err, operations.ErrConflict) || errors.Is(err, idempotency.ErrKeyReused) {
		writeError(c, stdhttp.StatusConflict, "configuration_conflict", "配置已经更新或请求编号已用于其他修改，请刷新后重试")
		return
	}
	if errors.Is(err, operations.ErrBusy) {
		writeError(c, stdhttp.StatusConflict, "configuration_busy", "其他配置正在提交，请稍后重试")
		return
	}
	if errors.Is(err, operations.ErrNotFound) {
		writeError(c, stdhttp.StatusNotFound, "not_found", "请求的业务对象不存在")
		return
	}
	var gatewayFailure *reconciliation.Failure
	if errors.As(err, &gatewayFailure) && (gatewayFailure.Kind == reconciliation.FailureAuthentication || gatewayFailure.Kind == reconciliation.FailureCompatibility) {
		writeError(c, stdhttp.StatusUnprocessableEntity, "gateway_check_failed", "站点凭证或兼容检查未通过")
		return
	}
	if errors.Is(err, onboarding.ErrInvalidInput) {
		writeError(c, stdhttp.StatusBadRequest, "invalid_request", strings.TrimPrefix(err.Error(), onboarding.ErrInvalidInput.Error()+": "))
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, stdhttp.StatusNotFound, "not_found", "请求的业务对象不存在")
		return
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		writeError(c, stdhttp.StatusConflict, "already_exists", "相同业务标识的对象已经存在")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(c, stdhttp.StatusGatewayTimeout, "request_timeout", "请求处理超时")
		return
	}
	h.logger.ErrorContext(c.Request.Context(), "request failed", "request_id", requestID(c), "error", err)
	writeError(c, stdhttp.StatusInternalServerError, "internal_error", "请求暂时无法完成")
}

func decodeJSON(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_json", "请求内容格式无效")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(c, stdhttp.StatusBadRequest, "invalid_json", "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
