package reconciliation

import (
	"context"
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
	TestChannel(context.Context, int64, string) error
	SetChannelEnabled(context.Context, int64, bool) error
}

type GatewayFactory interface {
	New(baseURL string, accessToken []byte) (Gateway, error)
}

func failuref(kind FailureKind, code, format string, args ...any) *Failure {
	return NewFailure(kind, code, fmt.Sprintf(format, args...), nil)
}
