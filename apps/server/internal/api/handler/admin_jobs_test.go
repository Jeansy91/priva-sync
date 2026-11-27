package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"privasync/server/internal/jobs"
)

func newJobsHandlerEnv(t *testing.T) (*AdminJobsHandler, *jobs.Registry) {
	t.Helper()
	pool := getTestPool(t)
	reg := jobs.NewRegistry(pool)
	return NewAdminJobsHandler(reg), reg
}

func TestAdminJobs_ListReturnsRegisteredJobs(t *testing.T) {
	h, reg := newJobsHandlerEnv(t)
	jobName := "handler_test_list_" + time.Now().Format("150405.000000000")
	reg.Register(jobName, "Test Job", func(ctx context.Context) (int, error) { return 5, nil })
	if _, err := reg.RunOnce(context.Background(), jobName); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = getTestPool(t).Exec(c, `DELETE FROM job_runs WHERE name=$1`, jobName)
	})

	rr := httptest.NewRecorder()
	h.List(rr, httptest.NewRequest("GET", "/api/v1/admin/jobs", nil))
	if rr.Code != 200 {
		t.Fatalf("code = %d, want 200", rr.Code)
	}
	var body struct {
		Jobs []struct {
			ID     string `json:"id"`
			Label  string `json:"label"`
			Status string `json:"status"`
			Items  int    `json:"items"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, j := range body.Jobs {
		if j.ID == jobName {
			found = true
			if j.Status != "success" || j.Items != 5 || j.Label != "Test Job" {
				t.Errorf("got %+v", j)
			}
		}
	}
	if !found {
		t.Fatalf("job %q missing in response", jobName)
	}
}

func TestAdminJobs_TriggerUnknownReturns404(t *testing.T) {
	h, _ := newJobsHandlerEnv(t)
	rr := httptest.NewRecorder()
	req := withURLParams(httptest.NewRequest("POST", "/api/v1/admin/jobs/nope/run", nil), "jobID", "nope")
	h.Trigger(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

func TestAdminJobs_TriggerRunsAsynchronously(t *testing.T) {
	h, reg := newJobsHandlerEnv(t)
	jobName := "handler_test_async_" + time.Now().Format("150405.000000000")
	done := make(chan struct{})
	reg.Register(jobName, "Async Job", func(ctx context.Context) (int, error) {
		close(done)
		return 2, nil
	})
	t.Cleanup(func() {
		_, _ = getTestPool(t).Exec(context.Background(), `DELETE FROM job_runs WHERE name=$1`, jobName)
	})

	rr := httptest.NewRecorder()
	req := withURLParams(httptest.NewRequest("POST", "/api/v1/admin/jobs/"+jobName+"/run", nil), "jobID", jobName)
	h.Trigger(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202", rr.Code)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("job did not run within 3s")
	}
}

func TestAdminJobs_TriggerWhileRunningReturns409(t *testing.T) {
	h, reg := newJobsHandlerEnv(t)
	jobName := "handler_test_busy_" + time.Now().Format("150405.000000000")
	release := make(chan struct{})
	entered := make(chan struct{})
	reg.Register(jobName, "Busy Job", func(ctx context.Context) (int, error) {
		close(entered)
		<-release
		return 0, nil
	})
	t.Cleanup(func() {
		close(release)
		_, _ = getTestPool(t).Exec(context.Background(), `DELETE FROM job_runs WHERE name=$1`, jobName)
	})

	rr := httptest.NewRecorder()
	req := withURLParams(httptest.NewRequest("POST", "/api/v1/admin/jobs/"+jobName+"/run", nil), "jobID", jobName)
	h.Trigger(rr, req)
	<-entered

	rr2 := httptest.NewRecorder()
	req2 := withURLParams(httptest.NewRequest("POST", "/api/v1/admin/jobs/"+jobName+"/run", nil), "jobID", jobName)
	h.Trigger(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("second code = %d, want 409", rr2.Code)
	}
}
