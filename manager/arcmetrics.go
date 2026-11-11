package manager

import (
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/actions/scaleset"
	"github.com/breakawaydata/orchard-gh-bridge/metrics"
)

// arcMetrics exports scale set statistics and job events under the metric
// names and labels of GitHub's Actions Runner Controller listener, so the
// queries and dashboards built for ARC runners work for Orchard scale sets.
// Every field is nil when metrics are disabled; the metric types are nil-safe.
type arcMetrics struct {
	assignedJobs      *metrics.GaugeVec
	runningJobs       *metrics.GaugeVec
	registeredRunners *metrics.GaugeVec
	busyRunners       *metrics.GaugeVec
	idleRunners       *metrics.GaugeVec
	desiredRunners    *metrics.GaugeVec
	maxRunners        *metrics.GaugeVec

	startedJobs   *metrics.CounterVec
	completedJobs *metrics.CounterVec

	startupDuration   *metrics.HistogramVec
	executionDuration *metrics.HistogramVec
	queueDuration     *metrics.HistogramVec
}

var (
	arcScaleSetLabels = []string{"name", "organization"}
	arcJobLabels      = []string{
		"name", "organization", "repository", "job_name",
		"job_workflow_ref", "job_workflow_name", "job_workflow_target", "event_name",
	}
	arcJobResultLabels = append(append([]string(nil), arcJobLabels...), "job_result")
)

func newARCMetrics(reg *metrics.Registry) *arcMetrics {
	buckets := metrics.DefaultDurationBuckets
	return &arcMetrics{
		assignedJobs:      reg.NewGaugeVec("gha_assigned_jobs", "Number of jobs assigned to this scale set.", arcScaleSetLabels...),
		runningJobs:       reg.NewGaugeVec("gha_running_jobs", "Number of jobs running (or about to be run).", arcScaleSetLabels...),
		registeredRunners: reg.NewGaugeVec("gha_registered_runners", "Number of runners registered by the scale set.", arcScaleSetLabels...),
		busyRunners:       reg.NewGaugeVec("gha_busy_runners", "Number of registered runners running a job.", arcScaleSetLabels...),
		idleRunners:       reg.NewGaugeVec("gha_idle_runners", "Number of registered runners not running a job.", arcScaleSetLabels...),
		desiredRunners:    reg.NewGaugeVec("gha_desired_runners", "Number of runners desired by the scale set.", arcScaleSetLabels...),
		maxRunners:        reg.NewGaugeVec("gha_max_runners", "Maximum number of runners the scale set may run right now.", arcScaleSetLabels...),
		startedJobs:       reg.NewCounterVec("gha_started_jobs_total", "Total number of jobs started.", arcJobLabels...),
		completedJobs:     reg.NewCounterVec("gha_completed_jobs_total", "Total number of jobs completed.", arcJobResultLabels...),
		startupDuration: reg.NewHistogramVec("gha_job_startup_duration_seconds",
			"Time from the scale set getting a job to a runner being assigned to it, in seconds.", buckets, arcJobLabels...),
		executionDuration: reg.NewHistogramVec("gha_job_execution_duration_seconds",
			"Time a job ran on its runner, in seconds.", buckets, arcJobResultLabels...),
		queueDuration: reg.NewHistogramVec("orchard_gh_bridge_job_queue_duration_seconds",
			"Time from a job being queued on GitHub to a runner being assigned to it, in seconds.", buckets, arcJobLabels...),
	}
}

// scaleSetRecorder implements listener.MetricsRecorder for one scale set.
type scaleSetRecorder struct {
	m            *arcMetrics
	name         string
	organization string
}

func (m *arcMetrics) forScaleSet(name, githubConfigURL string) *scaleSetRecorder {
	return &scaleSetRecorder{m: m, name: name, organization: organizationFromConfigURL(githubConfigURL)}
}

func (r *scaleSetRecorder) RecordStatistics(s *scaleset.RunnerScaleSetStatistic) {
	if s == nil {
		return
	}
	r.m.assignedJobs.Set(float64(s.TotalAssignedJobs), r.name, r.organization)
	r.m.runningJobs.Set(float64(s.TotalRunningJobs), r.name, r.organization)
	r.m.registeredRunners.Set(float64(s.TotalRegisteredRunners), r.name, r.organization)
	r.m.busyRunners.Set(float64(s.TotalBusyRunners), r.name, r.organization)
	r.m.idleRunners.Set(float64(s.TotalIdleRunners), r.name, r.organization)
}

func (r *scaleSetRecorder) RecordJobStarted(msg *scaleset.JobStarted) {
	if msg == nil {
		return
	}
	labels := r.jobLabels(&msg.JobMessageBase)
	r.m.startedJobs.Inc(labels...)
	observeBetween(r.m.startupDuration, msg.ScaleSetAssignTime, msg.RunnerAssignTime, labels)
	observeBetween(r.m.queueDuration, msg.QueueTime, msg.RunnerAssignTime, labels)
}

func (r *scaleSetRecorder) RecordJobCompleted(msg *scaleset.JobCompleted) {
	if msg == nil {
		return
	}
	labels := append(r.jobLabels(&msg.JobMessageBase), msg.Result)
	r.m.completedJobs.Inc(labels...)
	observeBetween(r.m.executionDuration, msg.RunnerAssignTime, msg.FinishTime, labels)
}

func (r *scaleSetRecorder) RecordDesiredRunners(count int) {
	r.m.desiredRunners.Set(float64(count), r.name, r.organization)
}

func (r *scaleSetRecorder) setMaxRunners(count int) {
	r.m.maxRunners.Set(float64(count), r.name, r.organization)
}

func (r *scaleSetRecorder) jobLabels(msg *scaleset.JobMessageBase) []string {
	org := msg.OwnerName
	if org == "" {
		org = r.organization
	}
	workflowName, workflowTarget := splitWorkflowRef(msg.JobWorkflowRef)
	return []string{
		r.name, org, msg.RepositoryName, msg.JobDisplayName,
		msg.JobWorkflowRef, workflowName, workflowTarget, msg.EventName,
	}
}

// observeBetween records end-start in seconds; a missing or reversed pair of
// timestamps is skipped rather than recorded as a bogus duration.
func observeBetween(h *metrics.HistogramVec, start, end time.Time, labels []string) {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return
	}
	h.Observe(end.Sub(start).Seconds(), labels...)
}

// splitWorkflowRef turns "owner/repo/.github/workflows/ci.yaml@refs/heads/main"
// into ("ci", "heads/main"), the job_workflow_name and job_workflow_target
// labels ARC derives from the same field.
func splitWorkflowRef(ref string) (name, target string) {
	file, gitRef, _ := strings.Cut(ref, "@")
	base := path.Base(file)
	if base == "." || base == "/" {
		base = ""
	}
	name = strings.TrimSuffix(base, path.Ext(base))
	target = strings.TrimPrefix(gitRef, "refs/")
	return name, target
}

// organizationFromConfigURL returns the first path element of a scale set's
// githubConfigURL (the organization or enterprise slug).
func organizationFromConfigURL(configURL string) string {
	u, err := url.Parse(configURL)
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "/")
	if first == "enterprises" {
		return ""
	}
	return first
}
