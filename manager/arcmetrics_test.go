package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/breakawaydata/orchard-gh-bridge/metrics"
)

func TestSplitWorkflowRef(t *testing.T) {
	name, target := splitWorkflowRef("example-org/example-repo/.github/workflows/unit-tests.yaml@refs/heads/develop")
	if name != "unit-tests" || target != "heads/develop" {
		t.Errorf("got (%q, %q)", name, target)
	}
	name, target = splitWorkflowRef("")
	if name != "" || target != "" {
		t.Errorf("empty ref: got (%q, %q)", name, target)
	}
}

func TestOrganizationFromConfigURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/example-org":              "example-org",
		"https://github.com/example-org/example-repo": "example-org",
		"https://github.com/enterprises/acme":         "",
		"::not a url":                                 "",
	} {
		if got := organizationFromConfigURL(in); got != want {
			t.Errorf("organizationFromConfigURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScaleSetRecorder(t *testing.T) {
	reg := metrics.NewRegistry()
	rec := newARCMetrics(reg).forScaleSet("macos", "https://github.com/example-org")

	rec.RecordStatistics(&scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3, TotalRunningJobs: 2, TotalRegisteredRunners: 2, TotalBusyRunners: 2})
	rec.RecordDesiredRunners(3)
	rec.setMaxRunners(4)

	queued := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	base := scaleset.JobMessageBase{
		RepositoryName:     "example-repo",
		OwnerName:          "example-org",
		JobDisplayName:     "Unit Tests",
		JobWorkflowRef:     "example-org/example-repo/.github/workflows/unit-tests.yaml@refs/heads/develop",
		EventName:          "pull_request",
		QueueTime:          queued,
		ScaleSetAssignTime: queued.Add(5 * time.Second),
		RunnerAssignTime:   queued.Add(35 * time.Second),
		FinishTime:         queued.Add(35*time.Second + 6*time.Minute),
	}
	rec.RecordJobStarted(&scaleset.JobStarted{JobMessageBase: base})
	rec.RecordJobCompleted(&scaleset.JobCompleted{Result: "succeeded", JobMessageBase: base})

	jobLabels := []string{"macos", "example-org", "example-repo", "Unit Tests", base.JobWorkflowRef, "unit-tests", "heads/develop", "pull_request"}
	m := rec.m
	if v, _ := m.assignedJobs.Value("macos", "example-org"); v != 3 {
		t.Errorf("gha_assigned_jobs = %v, want 3", v)
	}
	if v, _ := m.maxRunners.Value("macos", "example-org"); v != 4 {
		t.Errorf("gha_max_runners = %v, want 4", v)
	}
	if v, _ := m.desiredRunners.Value("macos", "example-org"); v != 3 {
		t.Errorf("gha_desired_runners = %v, want 3", v)
	}
	if v := m.startedJobs.Value(jobLabels...); v != 1 {
		t.Errorf("gha_started_jobs_total = %v, want 1", v)
	}
	if v := m.completedJobs.Value(append(jobLabels, "succeeded")...); v != 1 {
		t.Errorf("gha_completed_jobs_total = %v, want 1", v)
	}

	var b strings.Builder
	if err := reg.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`gha_job_startup_duration_seconds_sum{name="macos",organization="example-org",repository="example-repo",job_name="Unit Tests",job_workflow_ref="example-org/example-repo/.github/workflows/unit-tests.yaml@refs/heads/develop",job_workflow_name="unit-tests",job_workflow_target="heads/develop",event_name="pull_request"} 30`,
		`orchard_gh_bridge_job_queue_duration_seconds_sum{name="macos",organization="example-org",repository="example-repo",job_name="Unit Tests",job_workflow_ref="example-org/example-repo/.github/workflows/unit-tests.yaml@refs/heads/develop",job_workflow_name="unit-tests",job_workflow_target="heads/develop",event_name="pull_request"} 35`,
		`gha_job_execution_duration_seconds_sum{name="macos",organization="example-org",repository="example-repo",job_name="Unit Tests",job_workflow_ref="example-org/example-repo/.github/workflows/unit-tests.yaml@refs/heads/develop",job_workflow_name="unit-tests",job_workflow_target="heads/develop",event_name="pull_request",job_result="succeeded"} 360`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestScaleSetRecorder_SkipsMissingTimes(t *testing.T) {
	reg := metrics.NewRegistry()
	rec := newARCMetrics(reg).forScaleSet("macos", "https://github.com/example-org")
	rec.RecordJobStarted(&scaleset.JobStarted{JobMessageBase: scaleset.JobMessageBase{RepositoryName: "r"}})
	rec.RecordJobStarted(nil)
	rec.RecordJobCompleted(nil)
	rec.RecordStatistics(nil)
	if n := rec.m.startupDuration.Count("macos", "", "r", "", "", "", "", ""); n != 0 {
		t.Errorf("startup duration observed %d times for a job without timestamps", n)
	}
}

func TestScaleSetRecorder_NilRegistry(t *testing.T) {
	rec := newARCMetrics(nil).forScaleSet("macos", "https://github.com/example-org")
	rec.RecordStatistics(&scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 1})
	rec.RecordDesiredRunners(1)
	rec.setMaxRunners(1)
	rec.RecordJobStarted(&scaleset.JobStarted{})
	rec.RecordJobCompleted(&scaleset.JobCompleted{})
}
