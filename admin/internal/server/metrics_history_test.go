package server

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestMetricsHistoryPersistAndAPI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOFIX_DATA_DIR", dir)
	s, _ := newSrv(t)
	if s.metricsHistory == nil {
		t.Fatal("metrics history store missing")
	}
	s.metricsHistory.append("fleet", metricsPoint{"cpuUsed": 1.5, "cpuLimit": 4.0, "ramUsed": 0.5, "ramLimit": 2.0})
	// Force a second retained point past warm interval by rewriting lastAt.
	s.metricsHistory.Lock()
	if pts := s.metricsHistory.Series["fleet"]; len(pts) > 0 {
		pts[0]["at"] = float64(time.Now().Add(-2 * time.Minute).UnixMilli())
		s.metricsHistory.Series["fleet"] = pts
	}
	s.metricsHistory.Unlock()
	s.metricsHistory.append("fleet", metricsPoint{"cpuUsed": 2.0, "cpuLimit": 4.0, "ramUsed": 0.7, "ramLimit": 2.0})

	reloaded := loadMetricsHistory(dir)
	pts := reloaded.query("fleet", time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	if len(pts) < 1 {
		t.Fatalf("expected points after reload, got %d", len(pts))
	}

	from := strconv.FormatInt(time.Now().Add(-time.Hour).UnixMilli(), 10)
	w := do(s.AdminHandler(), "GET", "/api/metrics/history?series=fleet&from="+from, "", tok)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var out struct {
		Count  int              `json:"count"`
		Points []map[string]any `json:"points"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count < 1 || len(out.Points) < 1 {
		t.Fatalf("expected history points: %+v", out)
	}
	if code := do(s.AdminHandler(), "GET", "/api/metrics/history?series=fleet", "", "").Code; code != 401 {
		t.Fatal("missing auth", code)
	}
}
