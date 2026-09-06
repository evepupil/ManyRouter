package runtimehealth

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/compatibility"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("runtime site not found")

type Repository interface {
	ReadRuntimeSystemFacts(context.Context, time.Time) (SystemFacts, error)
	ListRuntimeSiteFacts(context.Context) ([]SiteFacts, error)
}

type CompatibilityChecker interface {
	CheckSite(context.Context, uuid.UUID, string) (compatibility.Report, error)
}

type BuildInfo struct {
	Version string
	Commit  string
}

type Service struct {
	repository     Repository
	checker        CompatibilityChecker
	build          BuildInfo
	catalogVersion string
	startedAt      time.Time
	now            func() time.Time
}

func NewService(
	repository Repository,
	checker CompatibilityChecker,
	build BuildInfo,
	catalogVersion string,
	startedAt time.Time,
	now func() time.Time,
) (*Service, error) {
	if repository == nil || checker == nil || build.Version == "" || build.Commit == "" || catalogVersion == "" || now == nil {
		return nil, errors.New("runtime health dependencies are required")
	}
	return &Service{
		repository: repository, checker: checker, build: build, catalogVersion: catalogVersion,
		startedAt: startedAt.UTC(), now: now,
	}, nil
}

func (service *Service) Summary(ctx context.Context) (Snapshot, error) {
	now := service.now().UTC()
	systemFacts, err := service.repository.ReadRuntimeSystemFacts(ctx, now)
	if err != nil {
		return Snapshot{}, err
	}
	siteFacts, err := service.repository.ListRuntimeSiteFacts(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	systemStatus, systemReasons := evaluateSystem(systemFacts, now)
	result := Snapshot{
		Status: systemStatus, GeneratedAt: now,
		System: SystemSnapshot{
			Status: systemStatus, BuildVersion: service.build.Version, BuildCommit: service.build.Commit,
			StartedAt: service.startedAt, CompatibilityCatalogVersion: service.catalogVersion,
			Facts: systemFacts, Reasons: systemReasons,
		},
		Sites: make([]SiteSnapshot, 0, len(siteFacts)),
	}
	for _, facts := range siteFacts {
		status, reasons := evaluateSite(facts, now)
		result.Sites = append(result.Sites, SiteSnapshot{SiteFacts: facts, Status: status, Reasons: reasons})
		result.Status = moreSevere(result.Status, status)
	}
	return result, nil
}

func (service *Service) Detail(ctx context.Context, siteID uuid.UUID) (SiteSnapshot, error) {
	snapshot, err := service.Summary(ctx)
	if err != nil {
		return SiteSnapshot{}, err
	}
	for _, item := range snapshot.Sites {
		if item.SiteID == siteID {
			return item, nil
		}
	}
	return SiteSnapshot{}, ErrNotFound
}

func (service *Service) Check(ctx context.Context, siteID uuid.UUID, actor string) (SiteSnapshot, error) {
	if _, err := service.checker.CheckSite(ctx, siteID, actor); err != nil {
		return SiteSnapshot{}, err
	}
	return service.Detail(ctx, siteID)
}

func (service *Service) Prometheus(ctx context.Context) (string, error) {
	snapshot, err := service.Summary(ctx)
	if err != nil {
		return DatabaseFailurePrometheus(service.build.Version, service.build.Commit), err
	}
	return Prometheus(snapshot), nil
}

func evaluateSystem(facts SystemFacts, now time.Time) (Level, []Reason) {
	status := LevelNormal
	reasons := make([]Reason, 0)
	add := func(level Level, reason Reason) {
		status = moreSevere(status, level)
		reasons = append(reasons, reason)
	}
	if !facts.DatabaseUp {
		add(LevelFault, Reason{Code: "database_unavailable", Message: "控制面数据库不可用。", Action: "检查 PostgreSQL 和连接配置。"})
	}
	absoluteSkew := math.Abs(facts.DatabaseClockSkewSecond)
	if absoluteSkew > 30 {
		add(LevelFault, Reason{Code: "database_clock_skew", Message: "数据库时间与应用时间偏差超过 30 秒。", Action: "校准主机和数据库时间。"})
	} else if absoluteSkew > 5 {
		add(LevelAttention, Reason{Code: "database_clock_skew", Message: "数据库时间与应用时间偏差超过 5 秒。", Action: "检查主机时间同步。"})
	}
	if facts.Jobs.Failed > 0 {
		add(LevelFault, Reason{Code: "jobs_failed", Message: "最近 24 小时存在永久失败的后台任务。", Action: "查看任务错误并处理后重新执行。"})
	}
	if facts.Jobs.Retryable > 0 {
		add(LevelAttention, Reason{Code: "jobs_retrying", Message: "后台任务正在等待重试。", Action: "检查持续失败的任务和外部服务。"})
	}
	if facts.Jobs.OldestWaitingAt != nil {
		age := now.Sub(*facts.Jobs.OldestWaitingAt)
		if age > 30*time.Minute {
			add(LevelFault, Reason{Code: "jobs_stalled", Message: "最老等待任务已超过 30 分钟。", Action: "检查工作进程和队列。"})
		} else if age > 10*time.Minute {
			add(LevelAttention, Reason{Code: "jobs_delayed", Message: "最老等待任务已超过 10 分钟。", Action: "检查工作进程是否正常消费。"})
		}
	}
	return status, reasons
}

func evaluateSite(facts SiteFacts, now time.Time) (Level, []Reason) {
	if facts.SiteStatus == "disabled" {
		return LevelNormal, []Reason{}
	}
	status := LevelNormal
	reasons := make([]Reason, 0)
	add := func(level Level, reason Reason) {
		status = moreSevere(status, level)
		reasons = append(reasons, reason)
	}
	if facts.Compatibility == nil {
		add(LevelBlocked, Reason{Code: "compatibility_unknown", Message: "站点尚未完成兼容检查。", Action: "立即检查站点兼容性。"})
	} else {
		switch facts.Compatibility.Verdict {
		case compatibility.VerdictCompatible:
			age := now.Sub(facts.Compatibility.CheckedAt)
			if age > 2*time.Hour {
				add(LevelBlocked, Reason{Code: "compatibility_expired", Message: "站点兼容检查已超过 2 小时。", Action: "重新检查后再发布线路。"})
			} else if age > 30*time.Minute {
				add(LevelAttention, Reason{Code: "compatibility_stale", Message: "站点兼容检查接近过期。", Action: "安排重新检查。"})
			}
		case compatibility.VerdictUnreachable:
			add(LevelFault, Reason{Code: "compatibility_unreachable", Message: "无法连接站点管理接口。", Action: "检查站点服务、网络和凭证。"})
		default:
			add(LevelBlocked, Reason{Code: "compatibility_blocked", Message: "站点当前不允许发布新线路。", Action: "查看兼容详情并处理阻塞原因。"})
		}
	}
	if facts.RelationCount == 0 {
		add(LevelAttention, Reason{Code: "site_empty", Message: "站点尚未投放供应商。", Action: "完成首个供应商投放。"})
	} else if facts.Route.ConfirmedAt == nil {
		add(LevelBlocked, Reason{Code: "route_unconfirmed", Message: "站点没有已确认线路。", Action: "查看线路同步并完成确认。"})
	}
	switch facts.Route.LatestPlanStatus {
	case "failed", "uncertain":
		add(LevelBlocked, Reason{Code: "route_plan_failed", Message: "最新线路没有完成确认。", Action: "查看同步步骤并处理后重试。"})
	case "pending", "applying":
		if facts.Route.LatestPlanCreatedAt != nil && now.Sub(*facts.Route.LatestPlanCreatedAt) > 15*time.Minute {
			add(LevelBlocked, Reason{Code: "route_plan_stalled", Message: "最新线路长时间未完成。", Action: "检查同步任务和站点状态。"})
		}
	}
	switch facts.Route.LastSyncStatus {
	case "manual_required", "uncertain":
		add(LevelBlocked, Reason{Code: "sync_manual_required", Message: "最近同步需要人工处理。", Action: "查看同步错误和受管资源归属。"})
	case "retryable_failed":
		add(LevelAttention, Reason{Code: "sync_retrying", Message: "最近同步正在等待重试。", Action: "检查站点连接和重试时间。"})
	}
	if facts.Route.OldestPendingAt != nil {
		age := now.Sub(*facts.Route.OldestPendingAt)
		if age > 30*time.Minute {
			add(LevelFault, Reason{Code: "sync_stalled", Message: "站点待处理同步已超过 30 分钟。", Action: "检查工作进程和同步错误。"})
		} else if age > 10*time.Minute {
			add(LevelAttention, Reason{Code: "sync_delayed", Message: "站点同步等待时间偏长。", Action: "检查任务队列。"})
		}
	}
	if facts.Collection.DataGap {
		add(LevelAttention, Reason{Code: "collection_gap", Message: "站点日志采集记录到数据缺口。", Action: "查看采集状态并补齐事实。"})
	}
	if facts.Collection.ConsecutiveFailures >= 3 {
		add(LevelFault, Reason{Code: "collection_failed", Message: "站点日志已连续采集失败。", Action: "检查日志接口和站点凭证。"})
	} else if facts.RelationCount > 0 {
		addFreshness(&status, &reasons, now, facts.Collection.LastSuccessAt, "collection", "日志采集")
	}
	if facts.RelationCount > 0 {
		addFreshness(&status, &reasons, now, facts.Scoring.CompletedAt, "scoring", "评分")
	}
	if facts.Route.ConfirmedAt != nil {
		addFreshness(&status, &reasons, now, facts.Product.GeneratedAt, "product", "产品数据")
	}
	if facts.Automation.AutomaticStrategies > 0 {
		addFreshness(&status, &reasons, now, facts.Automation.LastCompletedAt, "automation", "自动维护")
	}
	if facts.ProblemSuppliers > 0 {
		add(LevelBlocked, Reason{Code: "supplier_requires_attention", Message: "站点存在同步失败或人工锁定的供应商。", Action: "进入供应商投放记录处理。"})
	}
	return status, reasons
}

func addFreshness(status *Level, reasons *[]Reason, now time.Time, timestamp *time.Time, code, label string) {
	if timestamp == nil {
		*status = moreSevere(*status, LevelAttention)
		*reasons = append(*reasons, Reason{Code: code + "_missing", Message: label + "尚无成功记录。", Action: "检查对应任务并立即执行一次。"})
		return
	}
	age := now.Sub(*timestamp)
	if age > time.Hour {
		*status = moreSevere(*status, LevelAttention)
		*reasons = append(*reasons, Reason{Code: code + "_stale", Message: label + "已超过 1 小时未更新。", Action: "检查对应任务和数据来源。"})
	} else if age > 15*time.Minute {
		*status = moreSevere(*status, LevelAttention)
		*reasons = append(*reasons, Reason{Code: code + "_aging", Message: label + "已超过 15 分钟未更新。", Action: "观察下一轮任务或立即刷新。"})
	}
}

func moreSevere(left, right Level) Level {
	severity := map[Level]int{LevelNormal: 0, LevelAttention: 1, LevelBlocked: 2, LevelFault: 3}
	if severity[right] > severity[left] {
		return right
	}
	return left
}
