package measurement

import (
	"regexp"
	"strings"
	"unicode"
)

const maxErrorSummaryRunes = 240

var (
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`)
	secretPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|authorization|secret|password)\s*[:=]\s*[^\s,;]+`)
	skKeyPattern  = regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{6,}`)
	codePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,79}$`)
)

func ClassifyError(stableCode string, httpStatus int, text string) ErrorFact {
	code := normalizeStableCode(stableCode)
	class, matched := errorClassFromCode(code)
	if !matched {
		class, matched = errorClassFromHTTP(httpStatus)
	}
	summary := SanitizeErrorText(text)
	if !matched {
		class, matched = errorClassFromText(summary)
	}
	if !matched {
		class = ErrorUnknown
	}
	if summary == "" {
		summary = "upstream request failed"
	}
	return ErrorFact{
		Class: class, Responsibility: defaultResponsibility(class), StableCode: code,
		Summary: summary, RuleVersion: ErrorClassificationRuleVersion,
	}
}

func SanitizeErrorText(text string) string {
	clean := bearerPattern.ReplaceAllString(text, "Bearer [redacted]")
	clean = secretPattern.ReplaceAllString(clean, "$1=[redacted]")
	clean = skKeyPattern.ReplaceAllString(clean, "[redacted]")
	clean = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return ' '
		}
		return value
	}, clean)
	clean = strings.Join(strings.Fields(clean), " ")
	runes := []rune(clean)
	if len(runes) > maxErrorSummaryRunes {
		clean = string(runes[:maxErrorSummaryRunes])
	}
	return clean
}

func normalizeStableCode(code string) string {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if !codePattern.MatchString(normalized) {
		return ""
	}
	return normalized
}

func errorClassFromCode(code string) (ErrorClass, bool) {
	switch code {
	case "rate_limit", "rate_limited", "too_many_requests", "insufficient_quota_rate_limit":
		return ErrorRateLimited, true
	case "authentication_error", "invalid_api_key", "unauthorized":
		return ErrorAuthentication, true
	case "insufficient_balance", "insufficient_quota", "insufficient_user_quota", "quota_exhausted":
		return ErrorBalanceExhausted, true
	case "timeout", "request_timeout", "context_deadline_exceeded", "upstream_timeout":
		return ErrorTimeout, true
	case "invalid_request", "bad_request", "invalid_parameter":
		return ErrorInvalidRequest, true
	case "model_not_found", "upstream_unavailable", "service_unavailable", "bad_gateway", "gateway_timeout", "connection_error":
		return ErrorUpstreamUnavailable, true
	case "stream_incomplete", "stream_interrupted":
		return ErrorStreamIncomplete, true
	case "client_cancelled", "client_canceled", "context_canceled", "user_cancelled", "user_canceled":
		return ErrorCancelled, true
	case "content_filter", "content_policy_violation", "moderation_blocked", "safety_rejected":
		return ErrorRejected, true
	default:
		return ErrorUnknown, false
	}
}

func errorClassFromHTTP(status int) (ErrorClass, bool) {
	switch status {
	case 401:
		return ErrorAuthentication, true
	case 402:
		return ErrorBalanceExhausted, true
	case 408, 504:
		return ErrorTimeout, true
	case 429:
		return ErrorRateLimited, true
	case 404:
		return ErrorUpstreamUnavailable, true
	}
	if status >= 500 && status < 600 {
		return ErrorUpstreamUnavailable, true
	}
	return ErrorUnknown, false
}

func errorClassFromText(text string) (ErrorClass, bool) {
	lower := strings.ToLower(text)
	for _, candidate := range []struct {
		class ErrorClass
		terms []string
	}{
		{ErrorRateLimited, []string{"rate limit", "too many requests", "限流", "请求过于频繁"}},
		{ErrorAuthentication, []string{"unauthorized", "invalid api key", "鉴权失败", "认证失败", "密钥无效"}},
		{ErrorBalanceExhausted, []string{"insufficient balance", "insufficient quota", "余额不足", "额度不足"}},
		{ErrorTimeout, []string{"timeout", "timed out", "deadline exceeded", "超时"}},
		{ErrorStreamIncomplete, []string{"stream interrupted", "stream incomplete", "流式响应中断"}},
		{ErrorCancelled, []string{"client cancelled", "client canceled", "user cancelled", "user canceled", "用户取消"}},
		{ErrorRejected, []string{"content filter", "content policy", "moderation blocked", "内容拒绝", "内容策略"}},
		{ErrorUpstreamUnavailable, []string{"connection refused", "connection reset", "service unavailable", "bad gateway", "上游不可用"}},
		{ErrorInvalidRequest, []string{"invalid request", "bad request", "参数无效"}},
	} {
		for _, term := range candidate.terms {
			if strings.Contains(lower, term) {
				return candidate.class, true
			}
		}
	}
	return ErrorUnknown, false
}

func (fact ErrorFact) ResolvedResponsibility() ErrorResponsibility {
	if fact.Responsibility.Valid() {
		return fact.Responsibility
	}
	return defaultResponsibility(fact.Class)
}

func (responsibility ErrorResponsibility) Valid() bool {
	switch responsibility {
	case ResponsibilityUser, ResponsibilitySupplier, ResponsibilitySite, ResponsibilityUnknown:
		return true
	default:
		return false
	}
}

func defaultResponsibility(class ErrorClass) ErrorResponsibility {
	switch class {
	case ErrorInvalidRequest, ErrorCancelled, ErrorRejected:
		return ResponsibilityUser
	case ErrorRateLimited, ErrorAuthentication, ErrorBalanceExhausted, ErrorTimeout,
		ErrorUpstreamUnavailable, ErrorStreamIncomplete:
		return ResponsibilitySupplier
	default:
		return ResponsibilityUnknown
	}
}
