package server

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/clofix/filevps/admin/internal/store"
)

func TestAuditAccountScopeAndDetail(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()

	regA := do(h, "POST", "/api/auth/register", `{"email":"audit-a@client.test","password":"client-password-123","account_name":"Audit Co A"}`, "")
	if regA.Code != 200 {
		t.Fatal(regA.Body.String())
	}
	var outA struct {
		Account store.Account `json:"account"`
		User    store.IAMUser `json:"user"`
	}
	if err := json.Unmarshal(regA.Body.Bytes(), &outA); err != nil || outA.Account.ID == "" {
		t.Fatal(regA.Body.String())
	}
	cookieA := sessionCookieFrom(regA)

	regB := do(h, "POST", "/api/auth/register", `{"email":"audit-b@client.test","password":"client-password-123","account_name":"Audit Co B"}`, "")
	if regB.Code != 200 {
		t.Fatal(regB.Body.String())
	}
	var outB struct {
		Account store.Account `json:"account"`
	}
	if err := json.Unmarshal(regB.Body.Bytes(), &outB); err != nil || outB.Account.ID == "" {
		t.Fatal(regB.Body.String())
	}

	_ = m.AuditScoped(outA.Account.ID, outA.User.ID, "audit-a", "iam.group.save", "grp-1", "Operators", `{"name":"Operators"}`)
	_ = m.AuditScoped(outB.Account.ID, "usr-b", "audit-b", "instance.create", "vps-b", "ok", "")
	_ = m.Audit("legacy", "system.tick", "ctl", "ok")

	adminList := do(h, "GET", "/api/audit?limit=50", "", tok)
	if adminList.Code != 200 {
		t.Fatal(adminList.Body.String())
	}
	var adminBody struct {
		Events []store.AuditEntry `json:"events"`
	}
	if err := json.Unmarshal(adminList.Body.Bytes(), &adminBody); err != nil || len(adminBody.Events) < 3 {
		t.Fatalf("admin should see all events: %s", adminList.Body.String())
	}

	rootList := doCookie(h, "GET", "/api/audit?limit=50", "", cookieA)
	if rootList.Code != 200 {
		t.Fatal(rootList.Body.String())
	}
	var rootBody struct {
		Events []store.AuditEntry `json:"events"`
	}
	if err := json.Unmarshal(rootList.Body.Bytes(), &rootBody); err != nil {
		t.Fatal(err)
	}
	for _, e := range rootBody.Events {
		if e.AccountID != outA.Account.ID {
			t.Fatalf("root saw foreign account event: %+v", e)
		}
	}
	if len(rootBody.Events) == 0 {
		t.Fatal("root expected own-account events")
	}

	id := rootBody.Events[0].ID
	detail := doCookie(h, "GET", "/api/audit/"+strconv.FormatInt(id, 10), "", cookieA)
	if detail.Code != 200 {
		t.Fatal(detail.Body.String())
	}
	var view map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if int64(view["id"].(float64)) != id || view["action"] != "iam.group.save" {
		t.Fatalf("detail: %s", detail.Body.String())
	}
	if view["summary"] == nil || view["actor_user"] == nil || view["account"] == nil {
		t.Fatalf("expected enriched summary/actor/account: %s", detail.Body.String())
	}

	var otherID int64
	for _, e := range adminBody.Events {
		if e.AccountID == outB.Account.ID {
			otherID = e.ID
			break
		}
	}
	if otherID == 0 {
		t.Fatal("missing account B event for denial check")
	}
	denied := doCookie(h, "GET", "/api/audit/"+strconv.FormatInt(otherID, 10), "", cookieA)
	if denied.Code != 404 {
		t.Fatalf("want 404 for other account detail, got %d %s", denied.Code, denied.Body.String())
	}
}

func TestAuditRetentionAndFilters(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()

	reg := do(h, "POST", "/api/auth/register", `{"email":"audit-ret@client.test","password":"client-password-123","account_name":"Retention Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	var out struct {
		Account store.Account `json:"account"`
		User    store.IAMUser `json:"user"`
	}
	if err := json.Unmarshal(reg.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookieFrom(reg)

	_ = m.AuditScoped(out.Account.ID, out.User.ID, "audit-ret", "iam.user.save", "usr-new", "ok", "")
	_ = m.AuditScoped(out.Account.ID, out.User.ID, "audit-ret", "instance.create", "vps-1", "ok", "")
	_ = m.AuditScopedAt(time.Now().UTC().Add(-100*24*time.Hour), out.Account.ID, out.User.ID, "audit-ret", "iam.old", "grp-old", "ok", "")
	_ = m.AuditScopedAt(time.Now().UTC().Add(-200*24*time.Hour), out.Account.ID, out.User.ID, "audit-ret", "iam.ancient", "grp-x", "ok", "")

	client := doCookie(h, "GET", "/api/audit?limit=50", "", cookie)
	var clientBody struct {
		Events        []store.AuditEntry `json:"events"`
		RetentionDays int                `json:"retention_days"`
	}
	if err := json.Unmarshal(client.Body.Bytes(), &clientBody); err != nil {
		t.Fatal(err)
	}
	if clientBody.RetentionDays != 90 {
		t.Fatalf("client retention: %d", clientBody.RetentionDays)
	}
	for _, e := range clientBody.Events {
		if e.Action == "iam.old" || e.Action == "iam.ancient" {
			t.Fatalf("client must not see beyond 90d: %+v", e)
		}
	}

	admin := do(h, "GET", "/api/audit?limit=50&action=iam.old", "", tok)
	var adminBody struct {
		Events        []store.AuditEntry `json:"events"`
		RetentionDays int                `json:"retention_days"`
	}
	if err := json.Unmarshal(admin.Body.Bytes(), &adminBody); err != nil {
		t.Fatal(err)
	}
	if adminBody.RetentionDays != 180 {
		t.Fatalf("admin retention: %d", adminBody.RetentionDays)
	}
	if len(adminBody.Events) != 1 || adminBody.Events[0].Action != "iam.old" {
		t.Fatalf("admin should see 100d-old iam.old: %s", admin.Body.String())
	}

	filtered := doCookie(h, "GET", "/api/audit?limit=50&action=instance.create", "", cookie)
	var filt struct {
		Events []store.AuditEntry `json:"events"`
	}
	json.Unmarshal(filtered.Body.Bytes(), &filt)
	if len(filt.Events) != 1 || filt.Events[0].Action != "instance.create" {
		t.Fatalf("action filter: %s", filtered.Body.String())
	}

	search := doCookie(h, "GET", "/api/audit?limit=50&q=usr-new", "", cookie)
	var searched struct {
		Events []store.AuditEntry `json:"events"`
	}
	json.Unmarshal(search.Body.Bytes(), &searched)
	if len(searched.Events) != 1 || searched.Events[0].Target != "usr-new" {
		t.Fatalf("q filter: %s", search.Body.String())
	}

	n, err := m.PurgeAuditBefore(time.Now().UTC().Add(-180 * 24 * time.Hour))
	if err != nil || n < 1 {
		t.Fatalf("purge ancient: n=%d err=%v", n, err)
	}
	left, _ := m.ListAuditQuery(store.AuditQuery{AccountIDs: []string{out.Account.ID}, Limit: 50})
	for _, e := range left {
		if e.Action == "iam.ancient" {
			t.Fatal("ancient row should be purged")
		}
	}
}

func TestAuditPagination100PerPage(t *testing.T) {
	s, m := newSrv(t)
	h := s.AdminHandler()
	reg := do(h, "POST", "/api/auth/register", `{"email":"audit-page@client.test","password":"client-password-123","account_name":"Page Co"}`, "")
	if reg.Code != 200 {
		t.Fatal(reg.Body.String())
	}
	var out struct {
		Account store.Account `json:"account"`
		User    store.IAMUser `json:"user"`
	}
	json.Unmarshal(reg.Body.Bytes(), &out)
	cookie := sessionCookieFrom(reg)
	for i := 0; i < 105; i++ {
		_ = m.AuditScoped(out.Account.ID, out.User.ID, "audit-page", "test.page", "t-"+strconv.Itoa(i), "ok", "")
	}
	page1 := doCookie(h, "GET", "/api/audit?page=1&action=test.page", "", cookie)
	var body1 struct {
		Events   []store.AuditEntry `json:"events"`
		Page     int                `json:"page"`
		PageSize int                `json:"page_size"`
		Total    int                `json:"total"`
		Pages    int                `json:"pages"`
	}
	if err := json.Unmarshal(page1.Body.Bytes(), &body1); err != nil {
		t.Fatal(err)
	}
	if body1.PageSize != 100 || len(body1.Events) != 100 || body1.Total != 105 || body1.Pages != 2 {
		t.Fatalf("page1: size=%d len=%d total=%d pages=%d body=%s", body1.PageSize, len(body1.Events), body1.Total, body1.Pages, page1.Body.String())
	}
	page2 := doCookie(h, "GET", "/api/audit?page=2&action=test.page", "", cookie)
	var body2 struct {
		Events []store.AuditEntry `json:"events"`
		Page   int                `json:"page"`
	}
	json.Unmarshal(page2.Body.Bytes(), &body2)
	if body2.Page != 2 || len(body2.Events) != 5 {
		t.Fatalf("page2: page=%d len=%d", body2.Page, len(body2.Events))
	}
	if body1.Events[0].ID == body2.Events[0].ID {
		t.Fatal("page 2 should not repeat page 1 first row")
	}
}
