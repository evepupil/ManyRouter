package measurement_test

import (
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/measurement"
)

func TestErrorClassificationUsesStructuredEvidenceBeforeText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		code   string
		status int
		text   string
		want   measurement.ErrorClass
		party  measurement.ErrorResponsibility
	}{
		{name: "stable code wins", code: "invalid_api_key", status: 429, text: "timeout", want: measurement.ErrorAuthentication, party: measurement.ResponsibilitySupplier},
		{name: "HTTP wins over text", status: 429, text: "invalid api key", want: measurement.ErrorRateLimited, party: measurement.ResponsibilitySupplier},
		{name: "clean text fallback", text: "upstream request timeout", want: measurement.ErrorTimeout, party: measurement.ResponsibilitySupplier},
		{name: "user input", code: "invalid_request", status: 400, want: measurement.ErrorInvalidRequest, party: measurement.ResponsibilityUser},
		{name: "missing sold model", code: "model_not_found", status: 404, want: measurement.ErrorUpstreamUnavailable, party: measurement.ResponsibilitySupplier},
		{name: "content rejection", code: "content_filter", status: 400, want: measurement.ErrorRejected, party: measurement.ResponsibilityUser},
		{name: "ambiguous forbidden", status: 403, text: "provider rejected the call", want: measurement.ErrorUnknown, party: measurement.ResponsibilityUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := measurement.ClassifyError(test.code, test.status, test.text)
			if got.Class != test.want || got.ResolvedResponsibility() != test.party || got.RuleVersion != measurement.ErrorClassificationRuleVersion {
				t.Fatalf("classification = %#v", got)
			}
		})
	}
}

func TestErrorTextIsBoundedAndRedacted(t *testing.T) {
	t.Parallel()
	raw := "Authorization=secret-value Bearer token-value api_key=sk-sensitive password=hunter2\n" + strings.Repeat("x", 400)
	clean := measurement.SanitizeErrorText(raw)
	for _, secret := range []string{"secret-value", "token-value", "sk-sensitive", "hunter2"} {
		if strings.Contains(clean, secret) {
			t.Fatalf("sanitized text retained %q", secret)
		}
	}
	if len([]rune(clean)) > 240 || strings.ContainsAny(clean, "\r\n") {
		t.Fatalf("sanitized text is not bounded: %q", clean)
	}
}
