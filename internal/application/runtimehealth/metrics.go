package runtimehealth

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func Prometheus(snapshot Snapshot) string {
	var output strings.Builder
	writeHelp(&output, "manyrouter_build_info", "ManyRouter build identity.", "gauge")
	fmt.Fprintf(&output, "manyrouter_build_info{version=%s,commit=%s} 1\n", prometheusLabel(snapshot.System.BuildVersion), prometheusLabel(snapshot.System.BuildCommit))
	writeHelp(&output, "manyrouter_database_up", "Whether the control database is reachable.", "gauge")
	fmt.Fprintf(&output, "manyrouter_database_up %d\n", boolNumber(snapshot.System.Facts.DatabaseUp))
	writeHelp(&output, "manyrouter_jobs", "Current background jobs by bounded state.", "gauge")
	fmt.Fprintf(&output, "manyrouter_jobs{state=\"waiting\"} %d\n", snapshot.System.Facts.Jobs.Waiting)
	fmt.Fprintf(&output, "manyrouter_jobs{state=\"running\"} %d\n", snapshot.System.Facts.Jobs.Running)
	fmt.Fprintf(&output, "manyrouter_jobs{state=\"retryable\"} %d\n", snapshot.System.Facts.Jobs.Retryable)
	fmt.Fprintf(&output, "manyrouter_jobs{state=\"failed\"} %d\n", snapshot.System.Facts.Jobs.Failed)
	writeHelp(&output, "manyrouter_site_compatible", "Whether the latest site compatibility check passed.", "gauge")
	writeHelp(&output, "manyrouter_site_sync_age_seconds", "Age of the latest confirmed site route, or -1 when absent.", "gauge")
	writeHelp(&output, "manyrouter_site_collection_age_seconds", "Age of the latest successful site collection, or -1 when absent.", "gauge")
	writeHelp(&output, "manyrouter_site_product_age_seconds", "Age of the latest site product snapshot, or -1 when absent.", "gauge")
	writeHelp(&output, "manyrouter_site_pending_operations", "Current pending site synchronization operations.", "gauge")
	for _, site := range snapshot.Sites {
		label := prometheusLabel(site.SiteCode)
		compatible := 0
		if site.Compatibility != nil && site.Compatibility.Verdict == "compatible" {
			compatible = 1
		}
		fmt.Fprintf(&output, "manyrouter_site_compatible{site=%s} %d\n", label, compatible)
		fmt.Fprintf(&output, "manyrouter_site_sync_age_seconds{site=%s} %s\n", label, ageValue(snapshot.GeneratedAt, site.Route.ConfirmedAt))
		fmt.Fprintf(&output, "manyrouter_site_collection_age_seconds{site=%s} %s\n", label, ageValue(snapshot.GeneratedAt, site.Collection.LastSuccessAt))
		fmt.Fprintf(&output, "manyrouter_site_product_age_seconds{site=%s} %s\n", label, ageValue(snapshot.GeneratedAt, site.Product.GeneratedAt))
		fmt.Fprintf(&output, "manyrouter_site_pending_operations{site=%s} %d\n", label, site.Route.PendingOperations)
	}
	return output.String()
}

func DatabaseFailurePrometheus(version, commit string) string {
	return "# HELP manyrouter_build_info ManyRouter build identity.\n" +
		"# TYPE manyrouter_build_info gauge\n" +
		fmt.Sprintf("manyrouter_build_info{version=%s,commit=%s} 1\n", prometheusLabel(version), prometheusLabel(commit)) +
		"# HELP manyrouter_database_up Whether the control database is reachable.\n" +
		"# TYPE manyrouter_database_up gauge\n" +
		"manyrouter_database_up 0\n"
}

func writeHelp(output *strings.Builder, name, help, metricType string) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, metricType)
}

func prometheusLabel(value string) string {
	return strconv.Quote(value)
}

func ageValue(now time.Time, value *time.Time) string {
	if value == nil {
		return "-1"
	}
	age := now.Sub(*value).Seconds()
	if age < 0 {
		age = 0
	}
	return strconv.FormatFloat(age, 'f', 0, 64)
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}
