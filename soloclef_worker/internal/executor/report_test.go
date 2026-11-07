package executor

import (
	"errors"
	"testing"
)

func TestTheReportIsTheLastThingTheProcessSaid(t *testing.T) {
	// A library that wrote to file descriptor 1 before the process redirected
	// it would otherwise make an honest report unreadable.
	report, err := parseReport([]byte("ffmpeg version 7.1\n{\"status\": \"SUCCESS\", \"result\": 3}\n"))
	if err != nil {
		t.Fatalf("parseReport: %v", err)
	}
	if report.Status != reportSuccess || report.Result != 3.0 {
		t.Errorf("report = %#v", report)
	}
}

func TestNoOutputAtAllIsNoReport(t *testing.T) {
	if _, err := parseReport([]byte("  \n\n")); !errors.Is(err, errNoReport) {
		t.Errorf("parseReport = %v, want errNoReport", err)
	}
}

func TestAStatusThisWorkerCannotActOnIsRefused(t *testing.T) {
	for _, line := range []string{
		`{"status": "PENDING"}`,
		`{"result": 1}`,
		`{"status": "SUCC`,
	} {
		if _, err := parseReport([]byte(line)); err == nil || errors.Is(err, errNoReport) {
			t.Errorf("parseReport(%s) = %v, want a complaint about the report itself", line, err)
		}
	}
}

func TestAFailureWithNothingToGoOnStillNamesSomething(t *testing.T) {
	value := (&Report{Status: reportFailure}).errorValue()
	if value["exc_type"] == "" || value["exc_module"] == "" {
		t.Errorf("errorValue = %#v", value)
	}
	if _, ok := value["exc_message"].([]any); !ok {
		t.Errorf("exc_message = %#v, want a list, as Celery stores an exception's args", value["exc_message"])
	}
}
