package reconciliation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
)

type FailureKind string

const (
	FailureConfiguration  FailureKind = "configuration"
	FailureAuthentication FailureKind = "authentication"
	FailureCompatibility  FailureKind = "compatibility"
	FailureRetryable      FailureKind = "retryable"
	FailureUncertain      FailureKind = "uncertain"
	FailureOwnership      FailureKind = "ownership"
	FailureManualLock     FailureKind = "manual_lock"
)

type Failure struct {
	Kind    FailureKind
	Code    string
	Message string
	Cause   error
}

func (e *Failure) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *Failure) Unwrap() error {
	return e.Cause
}

func NewFailure(kind FailureKind, code, message string, cause error) *Failure {
	return &Failure{Kind: kind, Code: code, Message: message, Cause: cause}
}

type ChannelStatus string

const (
	ChannelEnabled          ChannelStatus = "enabled"
	ChannelManuallyDisabled ChannelStatus = "manually_disabled"
	ChannelAutoDisabled     ChannelStatus = "auto_disabled"
	ChannelUnknown          ChannelStatus = "unknown"
)

type ActualChannel struct {
	ID           int64
	ManagedTag   string
	Name         string
	Protocol     string
	BaseURL      string
	Models       []string
	ModelMapping map[string]string
	Groups       []string
	Priority     int64
	Weight       int
	Status       ChannelStatus
}

type ActualState struct {
	Version          string
	GroupRatios      map[string]string
	UserUsableGroups map[string]string
	Channels         []ActualChannel
}

type Gateway interface {
	Probe(context.Context) (string, error)
	ReadActualState(context.Context) (ActualState, error)
	SetGroupRatios(context.Context, map[string]string) error
	SetUserUsableGroups(context.Context, map[string]string) error
	CreateChannel(context.Context, routing.DesiredChannel, []byte) error
	UpdateChannel(context.Context, int64, routing.DesiredChannel, []byte) error
	TestChannel(context.Context, int64, string, []byte) error
	SetChannelEnabled(context.Context, int64, bool) error
}

type GatewayFactory interface {
	New(baseURL string, accessToken []byte) (Gateway, error)
}

type SiteGatewayFactory interface {
	NewForSite(baseURL string, accessToken []byte, adminUserID int64) (Gateway, error)
}

type BillingBasisReader interface {
	ReadBillingBasis(context.Context) (map[string]json.RawMessage, string, error)
}

type StatusCodeRange struct {
	Start int
	End   int
}

type RetryPolicy struct {
	RetryTimes  int
	StatusCodes []StatusCodeRange
}

func (policy RetryPolicy) AllowsStatus(code int) bool {
	for _, candidate := range policy.StatusCodes {
		if code >= candidate.Start && code <= candidate.End {
			return true
		}
	}
	return false
}

type RetryPolicyReader interface {
	ReadRetryPolicy(context.Context) (RetryPolicy, error)
}

func failuref(kind FailureKind, code, format string, args ...any) *Failure {
	return NewFailure(kind, code, fmt.Sprintf(format, args...), nil)
}
