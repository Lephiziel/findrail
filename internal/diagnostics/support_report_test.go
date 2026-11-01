package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSupportReportIsBoundedWhitelist(t *testing.T) {
	actual := 7
	report, err := BuildSupportReport("test", "go1.test", "linux", "amd64", &actual, 7, []SupportCheck{{ID: "schema", Status: "ok", Code: "schema_supported"}}, map[string]int64{"sources": 2})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > SupportReportMaxBytes {
		t.Fatalf("report too large: %d", len(data))
	}
	for _, forbidden := range []string{"/private", "secret", "query", "nonce", "source_id", "document_id"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("report contains forbidden key/value %q: %s", forbidden, data)
		}
	}
	if report.ReportVersion != 1 || report.IndexSchema == nil || *report.IndexSchema != 7 {
		t.Fatalf("report facts missing: %+v", report)
	}
}
