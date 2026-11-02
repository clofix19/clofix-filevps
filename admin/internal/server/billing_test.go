package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	kubernetesState "github.com/clofix/filevps/admin/internal/kubernetes"
	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
	"github.com/clofix/filevps/worker/pkg/scheduler"
)

func TestPricingAndBillingSeparation(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	h := s.AdminHandler()
	if permissionResource("/api/pricing/plans") != "pricing" || permissionResource("/api/billing/invoices") != "billing" {
		t.Fatal("pricing and billing must stay separate IAM resources")
	}
	if page, title, _, ok := uiPage("/billing"); !ok || page != "billing" || title != "Billing & Payments" {
		t.Fatalf("billing page route missing: %s %s %v", page, title, ok)
	}
	catalog := do(h, "GET", "/api/pricing", "", tok)
	if catalog.Code != 200 {
		t.Fatal(catalog.Body.String())
	}
	var pricing struct {
		Plans     []map[string]any `json:"plans"`
		Resources []billingResource
		Gateway   map[string]any
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &pricing); err != nil {
		t.Fatal(err)
	}
	if len(pricing.Plans) != 0 || len(pricing.Resources) != 12 || pricing.Gateway["secret"] != nil || pricing.Gateway["charges_processed"] != false {
		t.Fatalf("unexpected catalog: %s", catalog.Body.String())
	}
	created := do(h, "POST", "/api/pricing/plans", `{"product":"vps","name":"Starter","description":"1 vCPU","currency":"USD","monthly_cents":1000,"hourly_cents":2,"cpu":1,"ram_mb":1024,"disk_mb":20480,"visible":true,"enabled":true}`, tok)
	if created.Code != 200 {
		t.Fatal(created.Body.String())
	}
	var plan struct {
		ID        string
		Published billingRates
	}
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil || plan.ID == "" || plan.Published.MonthlyCents != 1000 {
		t.Fatal(created.Body.String())
	}
	sub := do(h, "POST", "/api/billing/subscriptions", `{"owner":"acme","plan_id":"`+plan.ID+`"}`, tok)
	if sub.Code != 200 {
		t.Fatal(sub.Body.String())
	}
	var subscribed struct {
		Subscription billingSubscription
	}
	if err := json.Unmarshal(sub.Body.Bytes(), &subscribed); err != nil || subscribed.Subscription.MonthlyCents != 1000 {
		t.Fatal(sub.Body.String())
	}
	if missing := do(h, "PUT", "/api/pricing/plans/"+plan.ID, `{"product":"vps","name":"Starter","description":"1 vCPU","currency":"USD","monthly_cents":2500,"hourly_cents":2,"cpu":1,"ram_mb":1024,"disk_mb":20480,"visible":true,"enabled":true}`, tok); missing.Code != 400 || !strings.Contains(missing.Body.String(), "effective_from") {
		t.Fatal(missing.Code, missing.Body.String())
	}
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	pending := do(h, "PUT", "/api/pricing/plans/"+plan.ID, `{"product":"vps","name":"Starter","description":"1 vCPU","currency":"USD","monthly_cents":2500,"hourly_cents":2,"cpu":1,"ram_mb":1024,"disk_mb":20480,"visible":true,"enabled":true,"effective_from":"`+future+`"}`, tok)
	if pending.Code != 200 {
		t.Fatal(pending.Body.String())
	}
	var updated struct {
		Published billingRates
		Pending   *billingRates
	}
	if err := json.Unmarshal(pending.Body.Bytes(), &updated); err != nil || updated.Published.MonthlyCents != 1000 || updated.Pending == nil || updated.Pending.MonthlyCents != 2500 {
		t.Fatal(pending.Body.String())
	}
	again := do(h, "POST", "/api/billing/subscriptions", `{"owner":"beta","plan_id":"`+plan.ID+`"}`, tok)
	var later struct{ Subscription billingSubscription }
	if err := json.Unmarshal(again.Body.Bytes(), &later); err != nil || later.Subscription.MonthlyCents != 1000 {
		t.Fatal(again.Body.String())
	}
	immediate := do(h, "PUT", "/api/pricing/plans/"+plan.ID, `{"product":"vps","name":"Starter","description":"1 vCPU","currency":"USD","monthly_cents":3000,"hourly_cents":2,"cpu":1,"ram_mb":1024,"disk_mb":20480,"visible":true,"enabled":true,"effective_from":"`+time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)+`"}`, tok)
	if immediate.Code != 200 {
		t.Fatal(immediate.Body.String())
	}
	kept := do(h, "GET", "/api/billing", "", tok)
	var ledger struct {
		Subscriptions []billingSubscription
	}
	if err := json.Unmarshal(kept.Body.Bytes(), &ledger); err != nil {
		t.Fatal(err)
	}
	if len(ledger.Subscriptions) != 2 {
		t.Fatal(kept.Body.String())
	}
	for _, item := range ledger.Subscriptions {
		if item.MonthlyCents != 1000 {
			t.Fatalf("active subscription price changed: %+v", item)
		}
	}
	fresh := do(h, "POST", "/api/billing/subscriptions", `{"owner":"gamma","plan_id":"`+plan.ID+`"}`, tok)
	var newest struct{ Subscription billingSubscription }
	if err := json.Unmarshal(fresh.Body.Bytes(), &newest); err != nil || newest.Subscription.MonthlyCents != 3000 {
		t.Fatal(fresh.Body.String())
	}
	inv := do(h, "POST", "/api/billing/invoices", `{"subscription_id":"`+subscribed.Subscription.ID+`"}`, tok)
	if inv.Code != 200 {
		t.Fatal(inv.Body.String())
	}
	var drafted struct{ Invoice billingInvoice }
	json.Unmarshal(inv.Body.Bytes(), &drafted)
	if drafted.Invoice.AmountCents != 1000 || drafted.Invoice.Status != "draft" || drafted.Invoice.USDCents != 1000 || drafted.Invoice.BDTCents != 122000 || len(drafted.Invoice.Lines) == 0 {
		t.Fatal(inv.Body.String())
	}
	s.UIDir = "../../web/admin-ui"
	pdf := do(h, "GET", "/api/billing/invoices/"+drafted.Invoice.ID+"/pdf", "", tok)
	raw := pdf.Body.String()
	if pdf.Code != 200 || !strings.HasPrefix(raw, "%PDF-1.4") || !strings.Contains(pdf.Header().Get("Content-Type"), "application/pdf") || !strings.Contains(pdf.Header().Get("Content-Disposition"), drafted.Invoice.ID+".pdf") || !strings.Contains(raw, drafted.Invoice.ID) || !strings.Contains(raw, "DRAFT") || !strings.Contains(raw, "INVOICE") || !strings.Contains(raw, "BILL TO") || !strings.Contains(raw, "DESCRIPTION") || !strings.Contains(raw, "Total") || !strings.Contains(raw, "USD") || !strings.Contains(raw, "BDT") || !strings.Contains(raw, "/Im1") {
		t.Fatal(pdf.Code, pdf.Header().Get("Content-Type"), raw)
	}
	if missing := do(h, "GET", "/api/billing/invoices/inv-missing/pdf", "", tok); missing.Code != 404 {
		t.Fatal(missing.Code, missing.Body.String())
	}
	if refund := do(h, "POST", "/api/billing/refunds", `{"invoice_id":"`+drafted.Invoice.ID+`"}`, tok); refund.Code != 409 {
		t.Fatal(refund.Body.String())
	}
	pay := do(h, "POST", "/api/billing/payments", `{"invoice_id":"`+drafted.Invoice.ID+`","method":"manual"}`, tok)
	if pay.Code != 200 {
		t.Fatal(pay.Body.String())
	}
	if gateway := do(h, "PUT", "/api/pricing/gateway", `{"provider":"stripe","publishable_key":"pk_test","secret":"sk_secret","enabled":true}`, tok); gateway.Code != 200 || strings.Contains(gateway.Body.String(), "sk_secret") {
		t.Fatal(gateway.Body.String())
	}
	reload := do(h, "GET", "/api/pricing", "", tok)
	if strings.Contains(reload.Body.String(), "sk_secret") || !strings.Contains(reload.Body.String(), `"secret_configured":true`) {
		t.Fatal(reload.Body.String())
	}
	report := do(h, "GET", "/api/billing/reports", "", tok)
	var totals struct {
		Currencies []struct {
			PaidCents int64 `json:"paid_cents"`
		} `json:"currencies"`
		USDCents int64            `json:"usd_cents"`
		BDTCents int64            `json:"bdt_cents"`
		Months   []map[string]any `json:"months"`
	}
	if err := json.Unmarshal(report.Body.Bytes(), &totals); err != nil || len(totals.Currencies) != 1 || totals.Currencies[0].PaidCents != 1000 {
		t.Fatal(report.Body.String())
	}
	if totals.USDCents != 1000 || totals.BDTCents != 122000 || len(totals.Months) == 0 {
		t.Fatal("monthly USD/BDT analysis missing", report.Body.String())
	}
}

func TestBillingInvoiceResourceLinesAndPage(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, _ := newSrv(t)
	s.UIDir = "../../web/admin-ui"
	h := s.AdminHandler()
	page := do(h, "GET", "/billing", "", tok)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="billing-analysis"`) || !strings.Contains(page.Body.String(), `id="chart-billing-month"`) || !strings.Contains(page.Body.String(), `id="billing-invoice-detail"`) || !strings.Contains(page.Body.String(), `id="billing-inventory-table"`) || !strings.Contains(page.Body.String(), `id="billing-due-table"`) || !strings.Contains(page.Body.String(), `data-billing-range="30d"`) {
		t.Fatal("billing analysis UI missing", page.Code)
	}
	created := do(h, "POST", "/api/pricing/plans", `{"product":"vps","name":"Starter","description":"1 vCPU","currency":"USD","monthly_cents":1000,"hourly_cents":2,"cpu":1,"ram_mb":1024,"disk_mb":20480,"visible":true,"enabled":true}`, tok)
	var plan struct {
		ID string `json:"id"`
	}
	json.Unmarshal(created.Body.Bytes(), &plan)
	sub := do(h, "POST", "/api/billing/subscriptions", `{"owner":"acme","plan_id":"`+plan.ID+`","resource_id":"vps-web01"}`, tok)
	var subscribed struct{ Subscription billingSubscription }
	json.Unmarshal(sub.Body.Bytes(), &subscribed)
	inv := do(h, "POST", "/api/billing/invoices", `{"subscription_id":"`+subscribed.Subscription.ID+`"}`, tok)
	var drafted struct{ Invoice billingInvoice }
	json.Unmarshal(inv.Body.Bytes(), &drafted)
	if drafted.Invoice.ResourceID != "vps-web01" || drafted.Invoice.Product != "vps" || len(drafted.Invoice.Lines) == 0 || drafted.Invoice.Lines[0].ResourceID != "vps-web01" {
		t.Fatal("resource line missing", inv.Body.String())
	}
	item := billingInvoice{AmountCents: 12200, TaxCents: 0, Currency: "BDT"}
	stampInvoiceMoney(&item, 122)
	if item.BDTCents != 12200 || item.USDCents != 100 {
		t.Fatalf("BDT conversion: usd=%d bdt=%d", item.USDCents, item.BDTCents)
	}
	fx := do(h, "PUT", "/api/pricing/currency", `{"currency":"USD","tax_name":"VAT","tax_percent":0,"bdt_per_usd":110}`, tok)
	if fx.Code != 200 || !strings.Contains(fx.Body.String(), `"bdt_per_usd":110`) {
		t.Fatal(fx.Body.String())
	}
}

func TestInvoicePDFProfessionalLayout(t *testing.T) {
	inv := billingInvoice{
		ID: "inv-layoutcheck01", Owner: "acme", Description: "Starter monthly service",
		Currency: "USD", AmountCents: 1000, TaxCents: 50, Status: "draft", Created: time.Unix(1_700_000_000, 0).UTC(),
	}
	plain := string(invoicePDF(inv, "VAT", 0, nil))
	if !strings.HasPrefix(plain, "%PDF-1.4") || !strings.Contains(plain, "INVOICE") || !strings.Contains(plain, "BILL TO") || !strings.Contains(plain, "DESCRIPTION") || !strings.Contains(plain, "Total") || !strings.Contains(plain, "inv-layoutcheck01") || !strings.Contains(plain, "DRAFT") || !strings.Contains(plain, "acme") || strings.Contains(plain, "/Im1") {
		t.Fatal(plain)
	}
	logo := loadInvoiceLogo("../../web/admin-ui")
	if logo == nil || logo.Width != 400 || logo.Height != 149 {
		t.Fatal("expected CloFix PNG logo")
	}
	branded := string(invoicePDF(inv, "VAT", 200, logo))
	if !strings.Contains(branded, "/Im1") || !strings.Contains(branded, "/Subtype /Image") || !strings.Contains(branded, "Refunded") {
		t.Fatal("logo or refund line missing")
	}
}

func TestBillingInventoryStoppedVPSStorageOnly(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	h := s.AdminHandler()
	units := billingResourceUnits()
	items := []map[string]any{}
	rates := map[string]int64{"cpu": 100, "ram": 50, "ssd": 10, "volume": 25, "backup": 40, "lb": 300, "cks": 400, "efs": 15, "s3": 20, "app": 80, "ipv4": 5, "transfer": 1}
	for _, name := range billingResources {
		items = append(items, map[string]any{"resource": name, "unit": units[name], "monthly_cents": rates[name], "hourly_cents": 0, "currency": "USD", "enabled": true})
	}
	raw, _ := json.Marshal(map[string]any{"resources": items})
	if saved := do(h, "PUT", "/api/pricing/resources", string(raw), tok); saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	m.UpsertWorker(store.Worker{ID: "w1", Hostname: "edge-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	spec := proto.InstanceSpec{Hostname: "web", RAMMB: 1024, CPU: 2, DiskMB: 20480}
	m.SaveInstance(store.Instance{ID: "vps-run", WorkerID: "w1", State: proto.StateRunning, Spec: spec})
	stopped := spec
	stopped.Hostname = "idle"
	m.SaveInstance(store.Instance{ID: "vps-stop", WorkerID: "w1", State: proto.StateStopped, Spec: stopped})
	m.SaveVolume(store.Volume{ID: "vol-data", Name: "data", WorkerID: "w1", State: "READY", LimitMB: 10240})
	s.usageMu.Lock()
	s.backups["bkp-web"] = proto.BackupInfo{ID: "bkp-web", InstanceID: "vps-run", WorkerID: "w1", SizeBytes: 1 << 30, DiskMB: 10240, State: "READY"}
	s.usageMu.Unlock()
	s.balancers.Lock()
	s.balancers.Items["lb-edge"] = &loadBalancer{ID: "lb-edge", Name: "edge", Instances: []string{"vps-run"}}
	s.balancers.Unlock()
	s.efs.Lock()
	s.efs.Shares["efs-site"] = efsShare{ID: "efs-site", Name: "site", Attachments: []efsAttachment{{InstanceID: "vps-run", GuestPath: "/var/www/html"}}}
	s.efs.Unlock()
	s.kubernetes.Lock()
	s.kubernetes.Clusters["cks-edge"] = kubernetesState.Cluster{
		ID: "cks-edge", Name: "eks-lab", State: "RUNNING", ControlPlaneWorkerID: "w1",
		NodePool: kubernetesState.NodePool{ID: "default", Count: 1, CPU: 1, RAMMB: 1024, DiskMB: 2048},
	}
	s.kubernetes.Unlock()
	got := do(h, "GET", "/api/billing", "", tok)
	var ledger struct {
		Inventory []billingInventoryItem `json:"inventory"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &ledger); err != nil {
		t.Fatal(err)
	}
	byID := map[string]billingInventoryItem{}
	for _, item := range ledger.Inventory {
		byID[item.ID] = item
	}
	run, ok := byID["vps-run"]
	if !ok || run.WorkerName != "edge-worker" || run.Name != "web" || run.StorageOnly || run.AmountCents != 450 {
		t.Fatalf("running VPS: %+v", run)
	}
	stop, ok := byID["vps-stop"]
	if !ok || !stop.StorageOnly || stop.AmountCents != 200 || stop.WorkerName != "edge-worker" {
		t.Fatalf("stopped VPS should be disk only: %+v", stop)
	}
	for _, line := range stop.Lines {
		if line.UsageType == "cpu" || line.UsageType == "ram" {
			t.Fatalf("stopped VPS billed compute: %+v", stop.Lines)
		}
	}
	vol, ok := byID["vol-data"]
	if !ok || vol.AmountCents != 250 || vol.WorkerName != "edge-worker" {
		t.Fatalf("volume: %+v", vol)
	}
	bkp, ok := byID["bkp-web"]
	if !ok || bkp.AmountCents != 40 {
		t.Fatalf("backup: %+v", bkp)
	}
	lb, ok := byID["lb-edge"]
	if !ok || lb.AmountCents != 300 || lb.Name != "edge" {
		t.Fatalf("lb: %+v", lb)
	}
	efs, ok := byID["efs-site"]
	if !ok || efs.AmountCents != 15 || efs.Name != "site" {
		t.Fatalf("efs: %+v", efs)
	}
	cks, ok := byID["cks-edge"]
	if !ok || cks.AmountCents != 570 || cks.Name != "eks-lab" || cks.Product != "cks" || cks.Kind != "cks" {
		t.Fatalf("cks: %+v", cks)
	}
}

func billingResourcePayload(hourly, lowHourly int64, enabled bool) string {
	units := billingResourceUnits()
	items := []map[string]any{}
	for _, name := range billingResources {
		hourlyCents, low := int64(0), int64(0)
		if name == "cpu" {
			hourlyCents, low = hourly, lowHourly
		}
		items = append(items, map[string]any{
			"resource": name, "unit": units[name], "monthly_cents": 0, "hourly_cents": hourlyCents,
			"lowcost_monthly_cents": 0, "lowcost_hourly_cents": low, "currency": "USD", "enabled": enabled,
		})
	}
	raw, _ := json.Marshal(map[string]any{"resources": items, "default_tier": "standard"})
	return string(raw)
}

func TestBillingMeterDeletedUsageDue(t *testing.T) {
	t.Setenv("CLOFIX_DATA_DIR", t.TempDir())
	s, m := newSrv(t)
	h := s.AdminHandler()
	if saved := do(h, "PUT", "/api/pricing/resources", billingResourcePayload(600, 300, true), tok); saved.Code != 200 || !strings.Contains(saved.Body.String(), `"lowcost_hourly_cents":300`) {
		t.Fatal(saved.Body.String())
	}
	m.UpsertWorker(store.Worker{ID: "w1", Hostname: "edge-worker", Status: "active", LastHeartbeat: time.Now(), Capacity: scheduler.Capacity{RAMTotal: 8 << 30, HostReserve: 1 << 30, MemAvailable: 6 << 30, CPUCores: 8, DiskTotal: 100 << 30, DiskFree: 80 << 30}})
	m.SaveInstance(store.Instance{ID: "vps-run", WorkerID: "w1", State: proto.StateRunning, Spec: proto.InstanceSpec{Hostname: "web", RAMMB: 1024, CPU: 2, DiskMB: 20480}})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	s.meterBillingAt(now, s.scanBillingInventory())
	if err := m.DeleteInstance("vps-run"); err != nil {
		t.Fatal(err)
	}
	s.meterBillingAt(now.Add(10*time.Minute), s.scanBillingInventory())
	got := do(h, "GET", "/api/billing", "", tok)
	var ledger struct {
		Due struct {
			DueCents int64                 `json:"due_cents"`
			Sessions []billingUsageSession `json:"sessions"`
		} `json:"due"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Due.DueCents != 200 || len(ledger.Due.Sessions) != 1 || !ledger.Due.Sessions[0].Deleted || ledger.Due.Sessions[0].Minutes != 10 || ledger.Due.Sessions[0].ResourceID != "vps-run" {
		t.Fatalf("10-minute deleted usage: %+v %s", ledger.Due, got.Body.String())
	}
	inv := do(h, "POST", "/api/billing/invoices", `{"from_due":true,"owner":"platform"}`, tok)
	if inv.Code != 200 {
		t.Fatal(inv.Body.String())
	}
	var drafted struct{ Invoice billingInvoice }
	json.Unmarshal(inv.Body.Bytes(), &drafted)
	if drafted.Invoice.AmountCents != 200 || drafted.Invoice.ResourceID != "vps-run" || len(drafted.Invoice.Lines) != 1 || !strings.Contains(drafted.Invoice.Lines[0].Description, "deleted") {
		t.Fatal(inv.Body.String())
	}
	cleared := do(h, "GET", "/api/billing", "", tok)
	json.Unmarshal(cleared.Body.Bytes(), &ledger)
	if ledger.Due.DueCents != 0 {
		t.Fatalf("due should clear after invoice: %s", cleared.Body.String())
	}
	m.SaveInstance(store.Instance{ID: "vps-short", WorkerID: "w1", State: proto.StateRunning, Spec: proto.InstanceSpec{Hostname: "short", RAMMB: 1024, CPU: 2, DiskMB: 20480}})
	low := do(h, "PUT", "/api/pricing/resources", strings.Replace(billingResourcePayload(600, 300, true), `"default_tier":"standard"`, `"default_tier":"lowcost"`, 1), tok)
	if low.Code != 200 || !strings.Contains(low.Body.String(), `"default_tier":"lowcost"`) {
		t.Fatal(low.Body.String())
	}
	later := now.Add(20 * time.Minute)
	s.meterBillingAt(later, s.scanBillingInventory())
	if err := m.DeleteInstance("vps-short"); err != nil {
		t.Fatal(err)
	}
	s.meterBillingAt(later.Add(5*time.Minute), s.scanBillingInventory())
	again := do(h, "GET", "/api/billing", "", tok)
	json.Unmarshal(again.Body.Bytes(), &ledger)
	if ledger.Due.DueCents != 50 {
		t.Fatalf("5-minute low-cost deleted usage: %+v %s", ledger.Due, again.Body.String())
	}
}
