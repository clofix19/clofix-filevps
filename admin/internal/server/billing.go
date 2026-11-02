package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

var billingProducts = map[string]string{
	"vps":            "VPS",
	"database":       "Managed Database",
	"object-storage": "Object Storage",
	"backup":         "Backup",
	"network":        "Network",
	"license":        "License Plans",
	"volume":         "Volume",
	"efs":            "EFS",
	"load-balancer":  "Load Balancer",
	"application":    "Application",
	"cks":            "CKS",
}
var billingResources = []string{"cpu", "ram", "ssd", "cks", "lb", "transfer", "backup", "ipv4", "volume", "efs", "s3", "app"}
var billingGateways = map[string]bool{
	"none": true, "manual": true, "stripe": true, "sslcommerz": true,
	"bkash": true, "nagad": true,
}

type billingRates struct {
	MonthlyCents  int64     `json:"monthly_cents"`
	HourlyCents   int64     `json:"hourly_cents"`
	SetupCents    int64     `json:"setup_cents"`
	OverageCents  int64     `json:"overage_cents"`
	CPU           int64     `json:"cpu"`
	RAMMB         int64     `json:"ram_mb"`
	DiskMB        int64     `json:"disk_mb"`
	MaxInstances  int64     `json:"max_instances"`
	BandwidthGB   int64     `json:"bandwidth_gb"`
	Currency      string    `json:"currency"`
	EffectiveFrom time.Time `json:"effective_from"`
}
type billingPlan struct {
	ID          string        `json:"id"`
	Product     string        `json:"product"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Visible     bool          `json:"visible"`
	Enabled     bool          `json:"enabled"`
	Created     time.Time     `json:"created"`
	Updated     time.Time     `json:"updated"`
	Pending     *billingRates `json:"pending,omitempty"`
	billingRates
}
type billingResource struct {
	Resource            string `json:"resource"`
	Unit                string `json:"unit"`
	MonthlyCents        int64  `json:"monthly_cents"`
	HourlyCents         int64  `json:"hourly_cents"`
	LowcostMonthlyCents int64  `json:"lowcost_monthly_cents,omitempty"`
	LowcostHourlyCents  int64  `json:"lowcost_hourly_cents,omitempty"`
	Currency            string `json:"currency"`
	Enabled             bool   `json:"enabled"`
}
type billingDiscount struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Code        string    `json:"code"`
	Percent     float64   `json:"percent"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	AppliesTo   []string  `json:"applies_to"`
	Starts      time.Time `json:"starts"`
	Ends        time.Time `json:"ends"`
	Enabled     bool      `json:"enabled"`
	Created     time.Time `json:"created"`
}
type billingGateway struct {
	Provider       string `json:"provider"`
	PublishableKey string `json:"publishable_key"`
	Secret         string `json:"secret,omitempty"`
	Enabled        bool   `json:"enabled"`
}
type billingCurrency struct {
	Currency    string  `json:"currency"`
	TaxName     string  `json:"tax_name"`
	TaxPercent  float64 `json:"tax_percent"`
	BDTPerUSD   float64 `json:"bdt_per_usd,omitempty"`
	DefaultTier string  `json:"default_tier,omitempty"`
}
type billingNotify struct {
	InvoiceIssued        bool `json:"invoice_issued"`
	PaymentFailed        bool `json:"payment_failed"`
	SubscriptionExpiring bool `json:"subscription_expiring"`
}
type billingSubscription struct {
	ID           string     `json:"id"`
	Owner        string     `json:"owner"`
	Product      string     `json:"product"`
	ResourceID   string     `json:"resource_id,omitempty"`
	PlanID       string     `json:"plan_id"`
	PlanName     string     `json:"plan_name"`
	Status       string     `json:"status"`
	Currency     string     `json:"currency"`
	MonthlyCents int64      `json:"monthly_cents"`
	HourlyCents  int64      `json:"hourly_cents"`
	SetupCents   int64      `json:"setup_cents"`
	Started      time.Time  `json:"started"`
	Canceled     *time.Time `json:"canceled,omitempty"`
	Created      time.Time  `json:"created"`
}
type billingInvoiceLine struct {
	Product      string  `json:"product"`
	ResourceID   string  `json:"resource_id,omitempty"`
	ResourceName string  `json:"resource_name,omitempty"`
	WorkerID     string  `json:"worker_id,omitempty"`
	WorkerName   string  `json:"worker_name,omitempty"`
	UsageType    string  `json:"usage_type"`
	Description  string  `json:"description"`
	Quantity     float64 `json:"quantity"`
	Unit         string  `json:"unit"`
	UnitCents    int64   `json:"unit_cents"`
	AmountCents  int64   `json:"amount_cents"`
	USDCents     int64   `json:"usd_cents"`
	BDTCents     int64   `json:"bdt_cents"`
}
type billingInvoice struct {
	ID             string               `json:"id"`
	Owner          string               `json:"owner"`
	SubscriptionID string               `json:"subscription_id,omitempty"`
	ResourceID     string               `json:"resource_id,omitempty"`
	Product        string               `json:"product,omitempty"`
	Description    string               `json:"description"`
	Currency       string               `json:"currency"`
	AmountCents    int64                `json:"amount_cents"`
	TaxCents       int64                `json:"tax_cents"`
	USDCents       int64                `json:"usd_cents"`
	BDTCents       int64                `json:"bdt_cents"`
	FXBDTPerUSD    float64              `json:"fx_bdt_per_usd"`
	PeriodStart    time.Time            `json:"period_start"`
	PeriodEnd      time.Time            `json:"period_end"`
	Lines          []billingInvoiceLine `json:"lines,omitempty"`
	Status         string               `json:"status"`
	Created        time.Time            `json:"created"`
	Paid           *time.Time           `json:"paid,omitempty"`
}
type billingPayment struct {
	ID          string    `json:"id"`
	InvoiceID   string    `json:"invoice_id"`
	Owner       string    `json:"owner"`
	Method      string    `json:"method"`
	Currency    string    `json:"currency"`
	AmountCents int64     `json:"amount_cents"`
	Status      string    `json:"status"`
	Created     time.Time `json:"created"`
}
type billingWallet struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	Currency     string    `json:"currency"`
	BalanceCents int64     `json:"balance_cents"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}
type billingRefund struct {
	ID           string    `json:"id"`
	InvoiceID    string    `json:"invoice_id"`
	Owner        string    `json:"owner"`
	Currency     string    `json:"currency"`
	AmountCents  int64     `json:"amount_cents"`
	CreditWallet bool      `json:"credit_wallet"`
	Created      time.Time `json:"created"`
}
type billingCredit struct {
	ID          string    `json:"id"`
	WalletID    string    `json:"wallet_id"`
	Owner       string    `json:"owner"`
	Reason      string    `json:"reason"`
	Currency    string    `json:"currency"`
	AmountCents int64     `json:"amount_cents"`
	Created     time.Time `json:"created"`
}
type billingStore struct {
	sync.Mutex
	Plans         map[string]billingPlan         `json:"plans"`
	Resources     []billingResource              `json:"resources"`
	Discounts     map[string]billingDiscount     `json:"discounts"`
	Gateway       billingGateway                 `json:"gateway"`
	Currency      billingCurrency                `json:"currency"`
	Notifications billingNotify                  `json:"notifications"`
	Subscriptions map[string]billingSubscription `json:"subscriptions"`
	Invoices      map[string]billingInvoice      `json:"invoices"`
	Payments      map[string]billingPayment      `json:"payments"`
	Wallets       map[string]billingWallet       `json:"wallets"`
	Refunds       map[string]billingRefund       `json:"refunds"`
	Credits       map[string]billingCredit       `json:"credits"`
	Usage         map[string]billingUsageSession `json:"usage,omitempty"`
	file          string
}

func loadBilling(dir string) *billingStore {
	d := &billingStore{file: filepath.Join(dir, "billing.json")}
	if raw, err := os.ReadFile(d.file); err == nil {
		if json.Unmarshal(raw, d) != nil {
			d.Plans, d.Discounts, d.Subscriptions, d.Invoices, d.Payments, d.Wallets, d.Refunds, d.Credits = nil, nil, nil, nil, nil, nil, nil, nil
		}
	}
	d.ensure()
	return d
}
func (d *billingStore) ensure() {
	if d.Plans == nil {
		d.Plans = map[string]billingPlan{}
	}
	if d.Discounts == nil {
		d.Discounts = map[string]billingDiscount{}
	}
	if d.Subscriptions == nil {
		d.Subscriptions = map[string]billingSubscription{}
	}
	if d.Invoices == nil {
		d.Invoices = map[string]billingInvoice{}
	}
	if d.Payments == nil {
		d.Payments = map[string]billingPayment{}
	}
	if d.Wallets == nil {
		d.Wallets = map[string]billingWallet{}
	}
	if d.Refunds == nil {
		d.Refunds = map[string]billingRefund{}
	}
	if d.Credits == nil {
		d.Credits = map[string]billingCredit{}
	}
	if d.Usage == nil {
		d.Usage = map[string]billingUsageSession{}
	}
	d.Currency.DefaultTier = normalizeBillingTier(d.Currency.DefaultTier)
	if d.Gateway.Provider == "" {
		d.Gateway.Provider = "none"
	}
	if d.Currency.Currency == "" {
		d.Currency.Currency = "USD"
	}
	merged := mergeBillingResources(d.Resources, d.Currency.Currency)
	if d.file != "" && billingResourcesJSON(d.Resources) != billingResourcesJSON(merged) {
		d.Resources = merged
		_ = d.save()
	} else {
		d.Resources = merged
	}
}
func billingProductID(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "kubernetes", "k8s", "eks":
		return "cks"
	default:
		return strings.TrimSpace(id)
	}
}
func billingProductLabel(id string) string {
	id = billingProductID(id)
	if label := billingProducts[id]; label != "" {
		return label
	}
	return id
}
func billingResourceLabels() map[string]string {
	return map[string]string{"cpu": "vCPU", "ram": "RAM", "ssd": "SSD", "transfer": "Transfer", "backup": "Backup", "ipv4": "IPv4", "volume": "Volume", "efs": "EFS", "s3": "S3", "lb": "Load balancer", "cks": "CKS", "app": "Application"}
}
func billingResourceUnits() map[string]string {
	return map[string]string{"cpu": "vCPU", "ram": "GB", "ssd": "GB", "transfer": "GB", "backup": "GB", "ipv4": "address", "volume": "GB", "efs": "GB", "s3": "GB", "lb": "balancer", "cks": "CKS cluster", "app": "app"}
}
func defaultBillingResources(currency string) []billingResource {
	units := billingResourceUnits()
	out := make([]billingResource, 0, len(billingResources))
	for _, name := range billingResources {
		out = append(out, billingResource{Resource: name, Unit: units[name], Currency: currency})
	}
	return out
}
func mergeBillingResources(existing []billingResource, currency string) []billingResource {
	byName := map[string]billingResource{}
	for _, item := range existing {
		if item.Resource == "kubernetes" {
			item.Resource = "cks"
		}
		if item.Resource != "" {
			byName[item.Resource] = item
		}
	}
	out := make([]billingResource, 0, len(billingResources))
	for _, item := range defaultBillingResources(currency) {
		if old, ok := byName[item.Resource]; ok {
			if old.Resource == "cks" {
				if old.Unit == "" || old.Unit == "cluster" {
					old.Unit = item.Unit
				}
				if !old.Enabled && old.MonthlyCents == 0 && old.HourlyCents == 0 {
					old.Enabled = true
				}
			}
			out = append(out, old)
			continue
		}
		if item.Resource == "cks" {
			item.Enabled = true
		}
		out = append(out, item)
	}
	return out
}
func billingResourcesJSON(items []billingResource) string {
	raw, _ := json.Marshal(items)
	return string(raw)
}
func (d *billingStore) save() error {
	if err := os.MkdirAll(filepath.Dir(d.file), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err = os.WriteFile(d.file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(d.file+".tmp", d.file)
}

func (s *Server) billingRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/pricing", s.auth(s.getPricing))
	m.HandleFunc("POST /api/pricing/plans", s.auth(s.savePricingPlan))
	m.HandleFunc("PUT /api/pricing/plans/{id}", s.auth(s.savePricingPlan))
	m.HandleFunc("POST /api/pricing/plans/{id}/enable", s.auth(s.setPricingPlanEnabled))
	m.HandleFunc("PUT /api/pricing/resources", s.auth(s.savePricingResources))
	m.HandleFunc("POST /api/pricing/discounts", s.auth(s.savePricingDiscount))
	m.HandleFunc("PUT /api/pricing/discounts/{id}", s.auth(s.savePricingDiscount))
	m.HandleFunc("POST /api/pricing/discounts/{id}/enable", s.auth(s.setPricingDiscountEnabled))
	m.HandleFunc("PUT /api/pricing/gateway", s.auth(s.savePricingGateway))
	m.HandleFunc("PUT /api/pricing/currency", s.auth(s.savePricingCurrency))
	m.HandleFunc("PUT /api/pricing/notifications", s.auth(s.savePricingNotifications))
	m.HandleFunc("GET /api/billing", s.auth(s.getBilling))
	m.HandleFunc("GET /api/billing/reports", s.auth(s.getBillingReports))
	m.HandleFunc("POST /api/billing/subscriptions", s.auth(s.createBillingSubscription))
	m.HandleFunc("POST /api/billing/subscriptions/{id}/cancel", s.auth(s.cancelBillingSubscription))
	m.HandleFunc("POST /api/billing/invoices", s.auth(s.createBillingInvoice))
	m.HandleFunc("GET /api/billing/invoices/{id}/pdf", s.auth(s.downloadBillingInvoicePDF))
	m.HandleFunc("POST /api/billing/payments", s.auth(s.recordBillingPayment))
	m.HandleFunc("POST /api/billing/checkout", s.auth(s.startBillingCheckout))
	m.HandleFunc("POST /api/billing/wallet", s.auth(s.createBillingWallet))
	m.HandleFunc("POST /api/billing/wallet/{id}/credit", s.auth(s.creditBillingWallet))
	m.HandleFunc("POST /api/billing/wallet/{id}/pay-invoice", s.auth(s.payInvoiceFromWallet))
	m.HandleFunc("POST /api/billing/refunds", s.auth(s.createBillingRefund))
}

func (p *billingPlan) rates() billingRates {
	return billingRates{MonthlyCents: p.MonthlyCents, HourlyCents: p.HourlyCents, SetupCents: p.SetupCents, OverageCents: p.OverageCents, CPU: p.CPU, RAMMB: p.RAMMB, DiskMB: p.DiskMB, MaxInstances: p.MaxInstances, BandwidthGB: p.BandwidthGB, Currency: p.Currency, EffectiveFrom: p.EffectiveFrom}
}
func (p *billingPlan) apply(r billingRates) {
	p.MonthlyCents, p.HourlyCents, p.SetupCents, p.OverageCents = r.MonthlyCents, r.HourlyCents, r.SetupCents, r.OverageCents
	p.CPU, p.RAMMB, p.DiskMB, p.MaxInstances, p.BandwidthGB = r.CPU, r.RAMMB, r.DiskMB, r.MaxInstances, r.BandwidthGB
	p.Currency, p.EffectiveFrom = r.Currency, r.EffectiveFrom
}
func (p *billingPlan) promote(now time.Time) bool {
	if p.Pending == nil || p.Pending.EffectiveFrom.After(now) {
		return false
	}
	p.apply(*p.Pending)
	p.Pending = nil
	return true
}
func ratesEqual(a, b billingRates) bool {
	a.EffectiveFrom, b.EffectiveFrom = time.Time{}, time.Time{}
	return a == b
}
func validCurrency(v string) bool {
	if len(v) != 3 {
		return false
	}
	for _, r := range v {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func validOwner(v string) bool {
	if len(v) < 2 || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' && r != '@' {
			return false
		}
	}
	return true
}
func parseBillingTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
func moneyOK(v int64) bool { return v >= 0 && v <= 1_000_000_000_00 }
func limitOK(v int64) bool { return v >= 0 && v <= 1_000_000 }

const defaultBDTPerUSD = 122

func billingFX(c billingCurrency) float64 {
	if c.BDTPerUSD > 0 {
		return c.BDTPerUSD
	}
	return defaultBDTPerUSD
}

func convertUSDAndBDT(cents int64, currency string, bdtPerUSD float64) (usd, bdt int64) {
	if bdtPerUSD <= 0 {
		bdtPerUSD = defaultBDTPerUSD
	}
	switch strings.ToUpper(currency) {
	case "BDT":
		bdt = cents
		usd = int64(float64(cents)/bdtPerUSD + 0.5)
	default:
		usd = cents
		bdt = int64(float64(cents)*bdtPerUSD + 0.5)
	}
	return usd, bdt
}

func billingMonthBounds(t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0).Add(-time.Second)
}

func normalizeBillingTier(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "lowcost") {
		return "lowcost"
	}
	return "standard"
}
func resourceUnitRate(item billingResource, hourly, lowcost bool) int64 {
	if !item.Enabled {
		return 0
	}
	switch {
	case hourly && lowcost && item.LowcostHourlyCents > 0:
		return item.LowcostHourlyCents
	case hourly:
		return item.HourlyCents
	case lowcost && item.LowcostMonthlyCents > 0:
		return item.LowcostMonthlyCents
	default:
		return item.MonthlyCents
	}
}
func resourceMonthlyCharge(resources []billingResource, name string, qty float64) int64 {
	return resourceChargeAt(resources, qty, false, false, name)
}
func resourceCharge(resources []billingResource, qty float64, names ...string) int64 {
	return resourceChargeAt(resources, qty, false, false, names...)
}
func resourceChargeAt(resources []billingResource, qty float64, hourly, lowcost bool, names ...string) int64 {
	if qty <= 0 {
		return 0
	}
	for _, name := range names {
		for _, item := range resources {
			if item.Resource != name {
				continue
			}
			unit := resourceUnitRate(item, hourly, lowcost)
			if unit > 0 {
				return int64(qty*float64(unit) + 0.5)
			}
		}
	}
	return 0
}

func pricedInvoiceLine(product, resourceID, usage, desc, unit string, qty float64, amount int64, currency string, rate float64) billingInvoiceLine {
	if qty <= 0 {
		qty = 1
	}
	usd, bdt := convertUSDAndBDT(amount, currency, rate)
	return billingInvoiceLine{
		Product: product, ResourceID: resourceID, UsageType: usage, Description: desc,
		Quantity: qty, Unit: unit, UnitCents: int64(float64(amount)/qty + 0.5), AmountCents: amount, USDCents: usd, BDTCents: bdt,
	}
}

func buildInvoiceLines(sub billingSubscription, plan billingPlan, resources []billingResource, amount int64, desc, currency string, rate float64) []billingInvoiceLine {
	product := sub.Product
	if product == "" {
		product = plan.Product
	}
	if product == "" {
		product = "vps"
	}
	if desc == "" {
		desc = sub.PlanName
		if desc == "" {
			desc = "CloFix Cloud service"
		}
	}
	lines := []billingInvoiceLine{}
	remaining := amount
	if sub.ID != "" {
		cpu := resourceMonthlyCharge(resources, "cpu", float64(plan.CPU))
		ram := resourceMonthlyCharge(resources, "ram", float64(plan.RAMMB)/1024)
		ssd := resourceMonthlyCharge(resources, "ssd", float64(plan.DiskMB)/1024)
		parts := []struct {
			name, usage, unit string
			qty               float64
			cents             int64
		}{
			{"CPU", "cpu", "vCPU-month", float64(plan.CPU), cpu},
			{"RAM", "ram", "GB-month", float64(plan.RAMMB) / 1024, ram},
			{"SSD", "ssd", "GB-month", float64(plan.DiskMB) / 1024, ssd},
		}
		sum := cpu + ram + ssd
		if sum > 0 && sum <= amount {
			for _, part := range parts {
				if part.cents <= 0 {
					continue
				}
				lines = append(lines, pricedInvoiceLine(product, sub.ResourceID, part.usage, billingProductLabel(product)+" "+part.name+" · "+desc, part.unit, part.qty, part.cents, currency, rate))
				remaining -= part.cents
			}
		}
	}
	if remaining > 0 || len(lines) == 0 {
		usage := "monthly"
		unit := "month"
		qty := 1.0
		if sub.ID == "" {
			usage, unit = "charge", "item"
		}
		lines = append(lines, pricedInvoiceLine(product, sub.ResourceID, usage, desc, unit, qty, remaining, currency, rate))
	}
	return lines
}

func stampInvoiceMoney(inv *billingInvoice, rate float64) {
	if inv == nil {
		return
	}
	if inv.FXBDTPerUSD > 0 {
		rate = inv.FXBDTPerUSD
	} else {
		if rate <= 0 {
			rate = defaultBDTPerUSD
		}
		inv.FXBDTPerUSD = rate
	}
	if inv.USDCents == 0 && inv.BDTCents == 0 {
		inv.USDCents, inv.BDTCents = convertUSDAndBDT(inv.AmountCents+inv.TaxCents, inv.Currency, rate)
	}
	if len(inv.Lines) == 0 {
		inv.Lines = buildInvoiceLines(billingSubscription{ResourceID: inv.ResourceID, Product: inv.Product}, billingPlan{}, nil, inv.AmountCents, inv.Description, inv.Currency, rate)
	}
	if inv.PeriodStart.IsZero() {
		inv.PeriodStart, inv.PeriodEnd = billingMonthBounds(inv.Created)
	}
}

func enrichInvoice(inv billingInvoice, rate float64) billingInvoice {
	stampInvoiceMoney(&inv, rate)
	return inv
}

func (s *Server) getPricing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.pricingView())
}
func pricedResourceViews(items []billingResource) []map[string]any {
	labels := billingResourceLabels()
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"resource": item.Resource, "label": labels[item.Resource], "unit": item.Unit,
			"monthly_cents": item.MonthlyCents, "hourly_cents": item.HourlyCents,
			"lowcost_monthly_cents": item.LowcostMonthlyCents, "lowcost_hourly_cents": item.LowcostHourlyCents,
			"currency": item.Currency, "enabled": item.Enabled,
		})
	}
	return out
}
func (s *Server) pricingView() map[string]any {
	d := s.billing
	d.Lock()
	defer d.Unlock()
	d.ensure()
	now := time.Now().UTC()
	changed := false
	plans := make([]map[string]any, 0, len(d.Plans))
	for id, p := range d.Plans {
		if p.promote(now) {
			p.Updated = now
			d.Plans[id] = p
			changed = true
		}
		active := 0
		for _, sub := range d.Subscriptions {
			if sub.PlanID == p.ID && sub.Status == "active" {
				active++
			}
		}
		plans = append(plans, map[string]any{
			"id": p.ID, "product": billingProductID(p.Product), "name": p.Name, "description": p.Description,
			"visible": p.Visible, "enabled": p.Enabled, "created": p.Created, "updated": p.Updated,
			"published": p.rates(), "pending": p.Pending, "active_subscriptions": active,
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		a, b := plans[i], plans[j]
		if a["product"].(string) == b["product"].(string) {
			return a["name"].(string) < b["name"].(string)
		}
		return a["product"].(string) < b["product"].(string)
	})
	discounts := make([]billingDiscount, 0, len(d.Discounts))
	for _, item := range d.Discounts {
		discounts = append(discounts, item)
	}
	sort.Slice(discounts, func(i, j int) bool { return discounts[i].Name < discounts[j].Name })
	if changed {
		_ = d.save()
	}
	products := []map[string]string{}
	for id, label := range billingProducts {
		products = append(products, map[string]string{"id": id, "label": label})
	}
	sort.Slice(products, func(i, j int) bool { return products[i]["id"] < products[j]["id"] })
	return map[string]any{
		"products": products, "plans": plans, "resources": pricedResourceViews(d.Resources), "discounts": discounts,
		"gateway": d.Gateway.public(), "currency": d.Currency, "notifications": d.Notifications,
		"note": "Price edits never rewrite active subscription amounts. Set effective_from for the new catalog price.",
	}
}
func (g billingGateway) public() map[string]any {
	return map[string]any{
		"provider": g.Provider, "publishable_key": g.PublishableKey, "secret_configured": g.Secret != "",
		"enabled": g.Enabled, "charges_processed": false,
		"note": "Gateway credentials are stored on the controller. Card charges are not processed in this version.",
	}
}

type planWrite struct {
	Product       string `json:"product"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Currency      string `json:"currency"`
	MonthlyCents  int64  `json:"monthly_cents"`
	HourlyCents   int64  `json:"hourly_cents"`
	SetupCents    int64  `json:"setup_cents"`
	OverageCents  int64  `json:"overage_cents"`
	CPU           int64  `json:"cpu"`
	RAMMB         int64  `json:"ram_mb"`
	DiskMB        int64  `json:"disk_mb"`
	MaxInstances  int64  `json:"max_instances"`
	BandwidthGB   int64  `json:"bandwidth_gb"`
	Visible       bool   `json:"visible"`
	Enabled       bool   `json:"enabled"`
	EffectiveFrom string `json:"effective_from"`
}

func (s *Server) savePricingPlan(w http.ResponseWriter, r *http.Request) {
	var in planWrite
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&in) != nil {
		fail(w, 400, "invalid plan payload")
		return
	}
	in.Product = billingProductID(in.Product)
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if billingProducts[in.Product] == "" || in.Name == "" || len(in.Name) > 80 || len(in.Description) > 240 || !validCurrency(in.Currency) {
		fail(w, 400, "product, name, and a 3-letter currency are required")
		return
	}
	if !moneyOK(in.MonthlyCents) || !moneyOK(in.HourlyCents) || !moneyOK(in.SetupCents) || !moneyOK(in.OverageCents) || !limitOK(in.CPU) || !limitOK(in.RAMMB) || !limitOK(in.DiskMB) || !limitOK(in.MaxInstances) || !limitOK(in.BandwidthGB) {
		fail(w, 400, "rates and resource values must be zero or positive and within range")
		return
	}
	when, ok := parseBillingTime(in.EffectiveFrom)
	if !ok {
		fail(w, 400, "effective_from must be RFC3339 or YYYY-MM-DDTHH:MM")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	now := time.Now().UTC()
	id := r.PathValue("id")
	old, exists := d.Plans[id]
	if id != "" && !exists {
		fail(w, 404, "plan not found")
		return
	}
	next := billingRates{MonthlyCents: in.MonthlyCents, HourlyCents: in.HourlyCents, SetupCents: in.SetupCents, OverageCents: in.OverageCents, CPU: in.CPU, RAMMB: in.RAMMB, DiskMB: in.DiskMB, MaxInstances: in.MaxInstances, BandwidthGB: in.BandwidthGB, Currency: in.Currency, EffectiveFrom: when}
	if !exists {
		if when.IsZero() {
			when = now
			next.EffectiveFrom = when
		}
		plan := billingPlan{ID: "pln-" + randHex(8), Product: in.Product, Name: in.Name, Description: in.Description, Visible: in.Visible, Enabled: in.Enabled, Created: now, Updated: now}
		plan.apply(next)
		d.Plans[plan.ID] = plan
		if err := d.save(); err != nil {
			delete(d.Plans, plan.ID)
			fail(w, 500, "could not save plan")
			return
		}
		s.Store.Audit(principalFrom(r).Name, "pricing.plan.create", plan.ID, plan.Name)
		writeJSON(w, 200, s.unlockedPlanView(d, plan, now))
		return
	}
	old.promote(now)
	priceChange := !ratesEqual(old.rates(), next)
	if priceChange {
		if when.IsZero() {
			fail(w, 400, "set effective_from when changing catalog prices or allocations. Active subscriptions keep their current price.")
			return
		}
		if !when.After(now) {
			old.apply(next)
			old.Pending = nil
		} else {
			pending := next
			old.Pending = &pending
		}
	}
	old.Product, old.Name, old.Description = in.Product, in.Name, in.Description
	old.Visible, old.Enabled, old.Updated = in.Visible, in.Enabled, now
	prev := d.Plans[old.ID]
	d.Plans[old.ID] = old
	if err := d.save(); err != nil {
		d.Plans[old.ID] = prev
		fail(w, 500, "could not save plan")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "pricing.plan.update", old.ID, old.Name)
	writeJSON(w, 200, s.unlockedPlanView(d, old, now))
}
func (s *Server) unlockedPlanView(d *billingStore, p billingPlan, now time.Time) map[string]any {
	active := 0
	for _, sub := range d.Subscriptions {
		if sub.PlanID == p.ID && sub.Status == "active" {
			active++
		}
	}
	return map[string]any{"id": p.ID, "product": p.Product, "name": p.Name, "description": p.Description, "visible": p.Visible, "enabled": p.Enabled, "created": p.Created, "updated": p.Updated, "published": p.rates(), "pending": p.Pending, "active_subscriptions": active, "note": "Existing active subscriptions keep their snapshot price."}
}
func (s *Server) setPricingPlanEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.Enabled == nil {
		fail(w, 400, "enabled is required")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	p, ok := d.Plans[r.PathValue("id")]
	if !ok {
		fail(w, 404, "plan not found")
		return
	}
	p.Enabled = *in.Enabled
	p.Updated = time.Now().UTC()
	d.Plans[p.ID] = p
	if err := d.save(); err != nil {
		fail(w, 500, "could not save plan")
		return
	}
	writeJSON(w, 200, map[string]any{"id": p.ID, "enabled": p.Enabled})
}
func (s *Server) savePricingResources(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Resources   []billingResource `json:"resources"`
		DefaultTier string            `json:"default_tier"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&in) != nil || len(in.Resources) == 0 {
		fail(w, 400, "resources are required")
		return
	}
	seen := map[string]bool{}
	out := make([]billingResource, 0, len(billingResources))
	for _, item := range in.Resources {
		item.Resource = billingProductID(strings.TrimSpace(item.Resource))
		if item.Resource == "kubernetes" {
			item.Resource = "cks"
		}
		item.Unit = strings.TrimSpace(item.Unit)
		item.Currency = strings.ToUpper(strings.TrimSpace(item.Currency))
		if !containsString(billingResources, item.Resource) || item.Unit == "" || len(item.Unit) > 24 || !validCurrency(item.Currency) || !moneyOK(item.MonthlyCents) || !moneyOK(item.HourlyCents) || !moneyOK(item.LowcostMonthlyCents) || !moneyOK(item.LowcostHourlyCents) || seen[item.Resource] {
			fail(w, 400, "each resource needs a known type, unit, currency, and non-negative rates")
			return
		}
		seen[item.Resource] = true
		out = append(out, item)
	}
	for _, name := range billingResources {
		if !seen[name] {
			fail(w, 400, "all resource types are required: "+strings.Join(billingResources, ", "))
			return
		}
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	prev := d.Resources
	prevTier := d.Currency.DefaultTier
	d.Resources = out
	if in.DefaultTier != "" {
		d.Currency.DefaultTier = normalizeBillingTier(in.DefaultTier)
	}
	if err := d.save(); err != nil {
		d.Resources = prev
		d.Currency.DefaultTier = prevTier
		fail(w, 500, "could not save resource pricing")
		return
	}
	writeJSON(w, 200, map[string]any{"resources": d.Resources, "default_tier": d.Currency.DefaultTier})
}
func containsString(all []string, v string) bool {
	for _, item := range all {
		if item == v {
			return true
		}
	}
	return false
}
func (s *Server) savePricingDiscount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Code        string   `json:"code"`
		Currency    string   `json:"currency"`
		Starts      string   `json:"starts"`
		Ends        string   `json:"ends"`
		Percent     float64  `json:"percent"`
		AmountCents int64    `json:"amount_cents"`
		AppliesTo   []string `json:"applies_to"`
		Enabled     bool     `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		fail(w, 400, "invalid discount payload")
		return
	}
	in.Name, in.Code, in.Currency = strings.TrimSpace(in.Name), strings.ToUpper(strings.TrimSpace(in.Code)), strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Name == "" || len(in.Name) > 80 || in.Code == "" || len(in.Code) > 32 || !validCurrency(in.Currency) || in.Percent < 0 || in.Percent > 100 || !moneyOK(in.AmountCents) || (in.Percent == 0 && in.AmountCents == 0) {
		fail(w, 400, "name, code, currency, and a percent or amount are required")
		return
	}
	starts, ok1 := parseBillingTime(in.Starts)
	ends, ok2 := parseBillingTime(in.Ends)
	if !ok1 || !ok2 || (!ends.IsZero() && !starts.IsZero() && !ends.After(starts)) {
		fail(w, 400, "starts/ends must be valid and ends must be after starts")
		return
	}
	applies := []string{}
	seen := map[string]bool{}
	for _, product := range in.AppliesTo {
		product = billingProductID(product)
		if billingProducts[product] == "" || seen[product] {
			fail(w, 400, "applies_to must list known products")
			return
		}
		seen[product] = true
		applies = append(applies, product)
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	id := r.PathValue("id")
	old, exists := d.Discounts[id]
	if id != "" && !exists {
		fail(w, 404, "discount not found")
		return
	}
	item := billingDiscount{ID: id, Name: in.Name, Code: in.Code, Percent: in.Percent, AmountCents: in.AmountCents, Currency: in.Currency, AppliesTo: applies, Starts: starts, Ends: ends, Enabled: in.Enabled, Created: time.Now().UTC()}
	if !exists {
		item.ID = "dsc-" + randHex(8)
	} else {
		item.Created = old.Created
	}
	d.Discounts[item.ID] = item
	if err := d.save(); err != nil {
		if exists {
			d.Discounts[item.ID] = old
		} else {
			delete(d.Discounts, item.ID)
		}
		fail(w, 500, "could not save discount")
		return
	}
	writeJSON(w, 200, item)
}
func (s *Server) setPricingDiscountEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.Enabled == nil {
		fail(w, 400, "enabled is required")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	item, ok := d.Discounts[r.PathValue("id")]
	if !ok {
		fail(w, 404, "discount not found")
		return
	}
	item.Enabled = *in.Enabled
	d.Discounts[item.ID] = item
	if err := d.save(); err != nil {
		fail(w, 500, "could not save discount")
		return
	}
	writeJSON(w, 200, item)
}
func (s *Server) savePricingGateway(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider       string `json:"provider"`
		PublishableKey string `json:"publishable_key"`
		Secret         string `json:"secret"`
		Enabled        bool   `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		fail(w, 400, "invalid gateway payload")
		return
	}
	in.Provider = strings.TrimSpace(strings.ToLower(in.Provider))
	if !billingGateways[in.Provider] {
		fail(w, 400, "provider must be none, manual, stripe, or sslcommerz")
		return
	}
	if in.Enabled && in.Provider == "none" {
		fail(w, 400, "enable a real provider before turning the gateway on")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	prev := d.Gateway
	next := billingGateway{Provider: in.Provider, PublishableKey: strings.TrimSpace(in.PublishableKey), Secret: strings.TrimSpace(in.Secret), Enabled: in.Enabled}
	if next.Secret == "" {
		next.Secret = prev.Secret
	}
	d.Gateway = next
	if err := d.save(); err != nil {
		d.Gateway = prev
		fail(w, 500, "could not save gateway settings")
		return
	}
	writeJSON(w, 200, d.Gateway.public())
}
func (s *Server) savePricingCurrency(w http.ResponseWriter, r *http.Request) {
	var in billingCurrency
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid currency payload")
		return
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.TaxName = strings.TrimSpace(in.TaxName)
	in.DefaultTier = normalizeBillingTier(in.DefaultTier)
	if !validCurrency(in.Currency) || len(in.TaxName) > 40 || in.TaxPercent < 0 || in.TaxPercent > 100 || in.BDTPerUSD < 0 || in.BDTPerUSD > 10000 {
		fail(w, 400, "currency must be a 3-letter code, tax percent 0-100, and BDT per USD 0-10000")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	prev := d.Currency
	d.Currency = in
	if err := d.save(); err != nil {
		d.Currency = prev
		fail(w, 500, "could not save currency settings")
		return
	}
	writeJSON(w, 200, d.Currency)
}
func (s *Server) savePricingNotifications(w http.ResponseWriter, r *http.Request) {
	var in billingNotify
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid notification payload")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	prev := d.Notifications
	d.Notifications = in
	if err := d.save(); err != nil {
		d.Notifications = prev
		fail(w, 500, "could not save billing notifications")
		return
	}
	writeJSON(w, 200, map[string]any{"notifications": d.Notifications, "note": "These flags are stored only. No billing mailer is connected yet."})
}

func (s *Server) getBilling(w http.ResponseWriter, r *http.Request) {
	s.reconcileBillingMeter()
	view := s.billingView()
	p := principalFrom(r)
	view["inventory"] = s.filterInventory(s.scanBillingInventory(), p)
	if p.Kind != "admin" {
		view = s.filterBillingView(view, p)
	}
	writeJSON(w, 200, view)
}
func (s *Server) billingOwnerOK(p iamPrincipal, owner string) bool {
	if p.Kind == "admin" {
		return true
	}
	if owner == "" {
		return false
	}
	return s.canAccessAccount(p, owner) || owner == p.UserID
}
func (s *Server) filterInventory(items []billingInventoryItem, p iamPrincipal) []billingInventoryItem {
	if p.Kind == "admin" {
		return items
	}
	out := make([]billingInventoryItem, 0, len(items))
	for _, item := range items {
		if s.billingOwnerOK(p, item.Owner) {
			out = append(out, item)
		}
	}
	return out
}
func (s *Server) filterBillingView(view map[string]any, p iamPrincipal) map[string]any {
	filter := func(list []any, ownerOf func(any) string) []any {
		out := make([]any, 0, len(list))
		for _, item := range list {
			if s.billingOwnerOK(p, ownerOf(item)) {
				out = append(out, item)
			}
		}
		return out
	}
	_ = filter
	if subs, ok := view["subscriptions"].([]billingSubscription); ok {
		out := make([]billingSubscription, 0, len(subs))
		for _, item := range subs {
			if s.billingOwnerOK(p, item.Owner) {
				out = append(out, item)
			}
		}
		view["subscriptions"] = out
	}
	if invoices, ok := view["invoices"].([]billingInvoice); ok {
		out := make([]billingInvoice, 0, len(invoices))
		for _, item := range invoices {
			if s.billingOwnerOK(p, item.Owner) {
				out = append(out, item)
			}
		}
		view["invoices"] = out
	}
	if payments, ok := view["payments"].([]billingPayment); ok {
		out := make([]billingPayment, 0, len(payments))
		for _, item := range payments {
			if s.billingOwnerOK(p, item.Owner) {
				out = append(out, item)
			}
		}
		view["payments"] = out
	}
	if wallets, ok := view["wallets"].([]billingWallet); ok {
		out := make([]billingWallet, 0, len(wallets))
		for _, item := range wallets {
			if s.billingOwnerOK(p, item.Owner) {
				out = append(out, item)
			}
		}
		view["wallets"] = out
	}
	if refunds, ok := view["refunds"].([]billingRefund); ok {
		out := make([]billingRefund, 0, len(refunds))
		for _, item := range refunds {
			if s.billingOwnerOK(p, item.Owner) {
				out = append(out, item)
			}
		}
		view["refunds"] = out
	}
	if credits, ok := view["credits"].([]billingCredit); ok {
		out := make([]billingCredit, 0, len(credits))
		for _, item := range credits {
			if s.billingOwnerOK(p, item.Owner) {
				out = append(out, item)
			}
		}
		view["credits"] = out
	}
	if due, ok := view["due"].(map[string]any); ok {
		if sessions, ok := due["sessions"].([]billingUsageSession); ok {
			out := make([]billingUsageSession, 0, len(sessions))
			for _, item := range sessions {
				if s.billingOwnerOK(p, item.Owner) {
					out = append(out, item)
				}
			}
			due["sessions"] = out
			view["due"] = due
		}
	}
	return view
}
func (s *Server) billingView() map[string]any {
	d := s.billing
	d.Lock()
	defer d.Unlock()
	now := time.Now().UTC()
	changed := false
	for id, p := range d.Plans {
		if p.promote(now) {
			p.Updated = now
			d.Plans[id] = p
			changed = true
		}
	}
	if changed {
		_ = d.save()
	}
	subs := values(d.Subscriptions)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Created.After(subs[j].Created) })
	rate := billingFX(d.Currency)
	invoices := values(d.Invoices)
	for i := range invoices {
		invoices[i] = enrichInvoice(invoices[i], rate)
	}
	sort.Slice(invoices, func(i, j int) bool { return invoices[i].Created.After(invoices[j].Created) })
	payments := values(d.Payments)
	sort.Slice(payments, func(i, j int) bool { return payments[i].Created.After(payments[j].Created) })
	wallets := values(d.Wallets)
	sort.Slice(wallets, func(i, j int) bool { return wallets[i].Owner < wallets[j].Owner })
	refunds := values(d.Refunds)
	sort.Slice(refunds, func(i, j int) bool { return refunds[i].Created.After(refunds[j].Created) })
	credits := values(d.Credits)
	sort.Slice(credits, func(i, j int) bool { return credits[i].Created.After(credits[j].Created) })
	active, draft, paid := 0, 0, 0
	for _, sub := range subs {
		if sub.Status == "active" {
			active++
		}
	}
	for _, inv := range invoices {
		switch inv.Status {
		case "draft":
			draft++
		case "paid":
			paid++
		}
	}
	plans := []map[string]any{}
	for _, p := range d.Plans {
		if p.Enabled {
			plans = append(plans, map[string]any{"id": p.ID, "name": p.Name, "product": p.Product, "published": p.rates()})
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i]["name"].(string) < plans[j]["name"].(string) })
	products := []map[string]string{}
	for id, label := range billingProducts {
		products = append(products, map[string]string{"id": id, "label": label})
	}
	sort.Slice(products, func(i, j int) bool { return products[i]["id"] < products[j]["id"] })
	return map[string]any{
		"overview": map[string]any{
			"active_subscriptions": active, "draft_invoices": draft, "paid_invoices": paid,
			"wallets": len(wallets), "payments": len(payments), "refunds": len(refunds),
			"gateway": d.Gateway.public(), "currency": d.Currency,
			"note": "Financial lists stay empty until a subscription, invoice, or manual payment is recorded. Catalog prices live in Settings → Pricing & Plans. Hourly usage is added to Due bill even after a resource is deleted.",
		},
		"subscriptions": subs, "invoices": invoices, "payments": payments, "wallets": wallets,
		"refunds": refunds, "credits": credits, "plans": plans, "products": products, "reports": billingReport(invoices, refunds),
		"due": billingDueView(d.Usage, rate),
	}
}
func values[T any](in map[string]T) []T {
	out := make([]T, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}
func billingReport(invoices []billingInvoice, refunds []billingRefund) map[string]any {
	paid, refunded := map[string]int64{}, map[string]int64{}
	for _, inv := range invoices {
		if inv.Status == "paid" {
			paid[inv.Currency] += inv.AmountCents + inv.TaxCents
		}
	}
	for _, item := range refunds {
		refunded[item.Currency] += item.AmountCents
	}
	rows := []map[string]any{}
	seen := map[string]bool{}
	for currency := range paid {
		seen[currency] = true
	}
	for currency := range refunded {
		seen[currency] = true
	}
	currencies := make([]string, 0, len(seen))
	for currency := range seen {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	for _, currency := range currencies {
		rows = append(rows, map[string]any{"currency": currency, "paid_cents": paid[currency], "refunded_cents": refunded[currency], "net_cents": paid[currency] - refunded[currency]})
	}
	months := map[string]*billingMonthRow{}
	resources := map[string]*billingResourceRow{}
	var usdTotal, bdtTotal int64
	for _, inv := range invoices {
		if inv.Status != "paid" {
			continue
		}
		usdTotal += inv.USDCents
		bdtTotal += inv.BDTCents
		key := inv.Created.UTC().Format("2006-01")
		row := months[key]
		if row == nil {
			row = &billingMonthRow{Month: key, ByProduct: map[string]int64{}}
			months[key] = row
		}
		row.PaidCents += inv.AmountCents + inv.TaxCents
		row.USDCents += inv.USDCents
		row.BDTCents += inv.BDTCents
		product := inv.Product
		if product == "" {
			product = "other"
		}
		row.ByProduct[product] += inv.AmountCents
		for _, line := range inv.Lines {
			rkey := line.ResourceID
			if rkey == "" {
				rkey = line.UsageType
			}
			if rkey == "" {
				rkey = inv.ID
			}
			item := resources[inv.Owner+"|"+rkey]
			if item == nil {
				item = &billingResourceRow{Owner: inv.Owner, ResourceID: line.ResourceID, Product: line.Product, UsageType: line.UsageType}
				resources[inv.Owner+"|"+rkey] = item
			}
			item.AmountCents += line.AmountCents
			item.USDCents += line.USDCents
			item.BDTCents += line.BDTCents
		}
	}
	monthKeys := make([]string, 0, len(months))
	for key := range months {
		monthKeys = append(monthKeys, key)
	}
	sort.Strings(monthKeys)
	monthRows := make([]map[string]any, 0, len(monthKeys))
	for _, key := range monthKeys {
		row := months[key]
		monthRows = append(monthRows, map[string]any{"month": row.Month, "paid_cents": row.PaidCents, "usd_cents": row.USDCents, "bdt_cents": row.BDTCents, "by_product": row.ByProduct})
	}
	resourceRows := make([]map[string]any, 0, len(resources))
	for _, item := range resources {
		resourceRows = append(resourceRows, map[string]any{"owner": item.Owner, "resource_id": item.ResourceID, "product": item.Product, "usage_type": item.UsageType, "amount_cents": item.AmountCents, "usd_cents": item.USDCents, "bdt_cents": item.BDTCents})
	}
	sort.Slice(resourceRows, func(i, j int) bool {
		if resourceRows[i]["owner"].(string) == resourceRows[j]["owner"].(string) {
			return resourceRows[i]["resource_id"].(string) < resourceRows[j]["resource_id"].(string)
		}
		return resourceRows[i]["owner"].(string) < resourceRows[j]["owner"].(string)
	})
	return map[string]any{
		"currencies": rows, "months": monthRows, "resources": resourceRows,
		"usd_cents": usdTotal, "bdt_cents": bdtTotal,
		"note": "Totals include only recorded paid invoices minus refunds. Unpaid drafts are excluded. USD and BDT use the invoice FX snapshot.",
	}
}

type billingMonthRow struct {
	Month     string
	PaidCents int64
	USDCents  int64
	BDTCents  int64
	ByProduct map[string]int64
}
type billingResourceRow struct {
	Owner       string
	ResourceID  string
	Product     string
	UsageType   string
	AmountCents int64
	USDCents    int64
	BDTCents    int64
}

func (s *Server) getBillingReports(w http.ResponseWriter, r *http.Request) {
	d := s.billing
	d.Lock()
	defer d.Unlock()
	p := principalFrom(r)
	rate := billingFX(d.Currency)
	invoices := values(d.Invoices)
	refunds := values(d.Refunds)
	if p.Kind != "admin" {
		filteredInv := make([]billingInvoice, 0, len(invoices))
		for _, inv := range invoices {
			if s.billingOwnerOK(p, inv.Owner) {
				filteredInv = append(filteredInv, inv)
			}
		}
		invoices = filteredInv
		filteredRef := make([]billingRefund, 0, len(refunds))
		for _, item := range refunds {
			if s.billingOwnerOK(p, item.Owner) {
				filteredRef = append(filteredRef, item)
			}
		}
		refunds = filteredRef
	}
	for i := range invoices {
		invoices[i] = enrichInvoice(invoices[i], rate)
	}
	writeJSON(w, 200, billingReport(invoices, refunds))
}
func (s *Server) createBillingSubscription(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Owner      string `json:"owner"`
		PlanID     string `json:"plan_id"`
		ResourceID string `json:"resource_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid subscription payload")
		return
	}
	actor := principalFrom(r)
	in.Owner, in.PlanID, in.ResourceID = strings.TrimSpace(in.Owner), strings.TrimSpace(in.PlanID), strings.TrimSpace(in.ResourceID)
	if in.Owner == "" {
		in.Owner = actor.AccountID
	}
	if !s.canAccessAccount(actor, in.Owner) {
		fail(w, 403, "cannot create a subscription for this account")
		return
	}
	if !validOwner(in.Owner) || !strings.HasPrefix(in.PlanID, "pln-") {
		fail(w, 400, "owner and plan_id are required")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	now := time.Now().UTC()
	p, ok := d.Plans[in.PlanID]
	if !ok {
		fail(w, 404, "plan not found")
		return
	}
	if p.promote(now) {
		p.Updated = now
		d.Plans[p.ID] = p
	}
	if !p.Enabled {
		fail(w, 409, "plan is disabled")
		return
	}
	rates := p.rates()
	sub := billingSubscription{ID: "sub-" + randHex(8), Owner: in.Owner, Product: p.Product, ResourceID: in.ResourceID, PlanID: p.ID, PlanName: p.Name, Status: "active", Currency: rates.Currency, MonthlyCents: rates.MonthlyCents, HourlyCents: rates.HourlyCents, SetupCents: rates.SetupCents, Started: now, Created: now}
	d.Subscriptions[sub.ID] = sub
	if err := d.save(); err != nil {
		delete(d.Subscriptions, sub.ID)
		fail(w, 500, "could not save subscription")
		return
	}
	s.Store.Audit(principalFrom(r).Name, "billing.subscription.create", sub.ID, sub.Owner)
	writeJSON(w, 200, map[string]any{"subscription": sub, "note": "Price is snapshotted from the published catalog. Later plan edits do not change this subscription."})
}
func (s *Server) cancelBillingSubscription(w http.ResponseWriter, r *http.Request) {
	d := s.billing
	d.Lock()
	defer d.Unlock()
	sub, ok := d.Subscriptions[r.PathValue("id")]
	if !ok {
		fail(w, 404, "subscription not found")
		return
	}
	if !s.canAccessAccount(principalFrom(r), sub.Owner) {
		fail(w, 404, "subscription not found")
		return
	}
	if sub.Status != "active" {
		fail(w, 409, "subscription is not active")
		return
	}
	now := time.Now().UTC()
	sub.Status = "canceled"
	sub.Canceled = &now
	d.Subscriptions[sub.ID] = sub
	if err := d.save(); err != nil {
		fail(w, 500, "could not cancel subscription")
		return
	}
	writeJSON(w, 200, sub)
}
func (s *Server) createBillingInvoice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Owner          string `json:"owner"`
		SubscriptionID string `json:"subscription_id"`
		Description    string `json:"description"`
		AmountCents    int64  `json:"amount_cents"`
		ResourceID     string `json:"resource_id"`
		Product        string `json:"product"`
		FromDue        bool   `json:"from_due"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid invoice payload")
		return
	}
	p := principalFrom(r)
	in.Owner, in.SubscriptionID, in.Description = strings.TrimSpace(in.Owner), strings.TrimSpace(in.SubscriptionID), strings.TrimSpace(in.Description)
	in.ResourceID, in.Product = strings.TrimSpace(in.ResourceID), strings.TrimSpace(in.Product)
	if in.Owner == "" {
		in.Owner = p.AccountID
	}
	if len(in.ResourceID) > 80 {
		in.ResourceID = in.ResourceID[:80]
	}
	if in.FromDue {
		s.reconcileBillingMeter()
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	currency := d.Currency.Currency
	sub := billingSubscription{}
	plan := billingPlan{}
	var dueLines []billingInvoiceLine
	var dueKeys []string
	if in.FromDue {
		if in.Description == "" {
			in.Description = "Due hourly usage"
		}
		dueLines, dueKeys, in.AmountCents = takeDueUsage(d.Usage, in.Owner, billingFX(d.Currency), currency)
		if in.AmountCents == 0 {
			fail(w, 400, "no due usage to invoice")
			return
		}
		if in.Owner == "" {
			in.Owner = "platform"
			if sess, ok := d.Usage[dueKeys[0]]; ok && sess.Owner != "" {
				in.Owner = sess.Owner
			}
		}
		if in.ResourceID == "" && len(dueLines) == 1 {
			in.ResourceID, sub.ResourceID = dueLines[0].ResourceID, dueLines[0].ResourceID
			sub.Product = dueLines[0].Product
		}
	} else if in.SubscriptionID != "" {
		var ok bool
		sub, ok = d.Subscriptions[in.SubscriptionID]
		if !ok {
			fail(w, 404, "subscription not found")
			return
		}
		in.Owner = sub.Owner
		if in.Description == "" {
			in.Description = sub.PlanName + " subscription"
		}
		if in.AmountCents == 0 {
			in.AmountCents = sub.MonthlyCents
		}
		currency = sub.Currency
		plan = d.Plans[sub.PlanID]
	} else {
		sub.ResourceID, sub.Product = in.ResourceID, in.Product
	}
	if !s.canAccessAccount(p, in.Owner) {
		fail(w, 403, "cannot create an invoice for this account")
		return
	}
	if !validOwner(in.Owner) || in.Description == "" || len(in.Description) > 160 || !moneyOK(in.AmountCents) || in.AmountCents == 0 {
		restoreDueUsage(d.Usage, dueKeys, dueLines)
		fail(w, 400, "owner, description, and a positive amount are required")
		return
	}
	now := time.Now().UTC()
	rate := billingFX(d.Currency)
	periodStart, periodEnd := billingMonthBounds(now)
	tax := int64(float64(in.AmountCents) * d.Currency.TaxPercent / 100)
	lines := buildInvoiceLines(sub, plan, d.Resources, in.AmountCents, in.Description, currency, rate)
	if in.FromDue {
		lines = dueLines
	}
	inv := billingInvoice{
		ID: "inv-" + randHex(8), Owner: in.Owner, SubscriptionID: in.SubscriptionID, ResourceID: sub.ResourceID, Product: sub.Product,
		Description: in.Description, Currency: currency, AmountCents: in.AmountCents, TaxCents: tax,
		PeriodStart: periodStart, PeriodEnd: periodEnd, Status: "draft", Created: now,
		Lines: lines,
	}
	stampInvoiceMoney(&inv, rate)
	d.Invoices[inv.ID] = inv
	if err := d.save(); err != nil {
		delete(d.Invoices, inv.ID)
		restoreDueUsage(d.Usage, dueKeys, dueLines)
		fail(w, 500, "could not save invoice")
		return
	}
	writeJSON(w, 200, map[string]any{"invoice": inv, "note": "Draft invoices are bookkeeping only until a manual payment is recorded."})
}
func (s *Server) recordBillingPayment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InvoiceID string `json:"invoice_id"`
		Method    string `json:"method"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid payment payload")
		return
	}
	in.InvoiceID, in.Method = strings.TrimSpace(in.InvoiceID), strings.TrimSpace(strings.ToLower(in.Method))
	if in.Method == "" {
		in.Method = "manual"
	}
	switch in.Method {
	case "manual", "bkash", "nagad", "sslcommerz", "stripe", "wallet":
	default:
		fail(w, 400, "unsupported payment method")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	inv, ok := d.Invoices[in.InvoiceID]
	if !ok {
		fail(w, 404, "invoice not found")
		return
	}
	if !s.canAccessAccount(principalFrom(r), inv.Owner) {
		fail(w, 404, "invoice not found")
		return
	}
	if inv.Status != "draft" {
		fail(w, 409, "only draft invoices can be marked paid")
		return
	}
	now := time.Now().UTC()
	inv.Status = "paid"
	inv.Paid = &now
	pay := billingPayment{ID: "pay-" + randHex(8), InvoiceID: inv.ID, Owner: inv.Owner, Method: in.Method, Currency: inv.Currency, AmountCents: inv.AmountCents + inv.TaxCents, Status: "recorded", Created: now}
	d.Invoices[inv.ID] = inv
	d.Payments[pay.ID] = pay
	if err := d.save(); err != nil {
		inv.Status, inv.Paid = "draft", nil
		d.Invoices[inv.ID] = inv
		delete(d.Payments, pay.ID)
		fail(w, 500, "could not record payment")
		return
	}
	note := "Payment recorded for bookkeeping."
	if in.Method == "manual" {
		note = "Manual bookkeeping only. No card or bank charge was sent."
	}
	writeJSON(w, 200, map[string]any{"payment": pay, "invoice": inv, "note": note})
}

func (s *Server) startBillingCheckout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InvoiceID string `json:"invoice_id"`
		WalletID  string `json:"wallet_id"`
		Provider  string `json:"provider"`
		Amount    int64  `json:"amount_cents"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid checkout payload")
		return
	}
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	if !billingGateways[in.Provider] || in.Provider == "none" || in.Provider == "manual" {
		fail(w, 400, "provider must be bkash, nagad, sslcommerz, or stripe")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	if !d.Gateway.Enabled || d.Gateway.Provider != in.Provider {
		fail(w, 409, "enable this provider under Settings → Payment Gateway before checkout")
		return
	}
	amount := in.Amount
	owner := principalFrom(r).AccountID
	if in.InvoiceID != "" {
		inv, ok := d.Invoices[in.InvoiceID]
		if !ok || !s.canAccessAccount(principalFrom(r), inv.Owner) {
			fail(w, 404, "invoice not found")
			return
		}
		if inv.Status != "draft" {
			fail(w, 409, "only draft invoices can be checked out")
			return
		}
		amount = inv.AmountCents + inv.TaxCents
		owner = inv.Owner
	}
	if amount <= 0 {
		fail(w, 400, "amount_cents must be positive for wallet top-up")
		return
	}
	now := time.Now().UTC()
	intent := billingPayment{
		ID: "pay-" + randHex(8), InvoiceID: in.InvoiceID, Owner: owner, Method: in.Provider,
		Currency: d.Currency.Currency, AmountCents: amount, Status: "pending_provider", Created: now,
	}
	if intent.Currency == "" {
		intent.Currency = "BDT"
	}
	d.Payments[intent.ID] = intent
	if err := d.save(); err != nil {
		delete(d.Payments, intent.ID)
		fail(w, 500, "could not create checkout")
		return
	}
	writeJSON(w, 202, map[string]any{
		"payment": intent,
		"wallet_id": in.WalletID,
		"checkout": map[string]any{
			"provider": in.Provider,
			"status":   "pending_provider",
			"message":  "Redirect the customer to the provider checkout using the stored gateway credentials, then POST /api/billing/payments with method=" + in.Provider + " (or wallet pay-invoice) after the provider callback succeeds.",
		},
	})
}

func (s *Server) payInvoiceFromWallet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InvoiceID string `json:"invoice_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || strings.TrimSpace(in.InvoiceID) == "" {
		fail(w, 400, "invoice_id required")
		return
	}
	walletID := r.PathValue("id")
	d := s.billing
	d.Lock()
	defer d.Unlock()
	wallet, ok := d.Wallets[walletID]
	if !ok {
		fail(w, 404, "wallet not found")
		return
	}
	inv, ok := d.Invoices[in.InvoiceID]
	if !ok {
		fail(w, 404, "invoice not found")
		return
	}
	if !s.canAccessAccount(principalFrom(r), inv.Owner) || !s.canAccessAccount(principalFrom(r), wallet.Owner) {
		fail(w, 404, "invoice not found")
		return
	}
	if inv.Status != "draft" {
		fail(w, 409, "only draft invoices can be paid")
		return
	}
	if wallet.Currency != inv.Currency {
		fail(w, 409, "wallet currency must match the invoice")
		return
	}
	due := inv.AmountCents + inv.TaxCents
	if wallet.BalanceCents < due {
		fail(w, 409, "insufficient wallet balance; top up with bKash/Nagad/SSLCOMMERZ checkout first")
		return
	}
	now := time.Now().UTC()
	wallet.BalanceCents -= due
	wallet.Updated = now
	inv.Status = "paid"
	inv.Paid = &now
	pay := billingPayment{ID: "pay-" + randHex(8), InvoiceID: inv.ID, Owner: inv.Owner, Method: "wallet", Currency: inv.Currency, AmountCents: due, Status: "recorded", Created: now}
	d.Wallets[wallet.ID] = wallet
	d.Invoices[inv.ID] = inv
	d.Payments[pay.ID] = pay
	if err := d.save(); err != nil {
		fail(w, 500, "could not settle invoice from wallet")
		return
	}
	writeJSON(w, 200, map[string]any{"payment": pay, "invoice": inv, "wallet": wallet})
}
func (s *Server) createBillingWallet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Owner, Currency string
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid wallet payload")
		return
	}
	in.Owner, in.Currency = strings.TrimSpace(in.Owner), strings.ToUpper(strings.TrimSpace(in.Currency))
	d := s.billing
	d.Lock()
	defer d.Unlock()
	if in.Currency == "" {
		in.Currency = d.Currency.Currency
	}
	if !validOwner(in.Owner) || !validCurrency(in.Currency) {
		fail(w, 400, "owner and currency are required")
		return
	}
	for _, wallet := range d.Wallets {
		if wallet.Owner == in.Owner && wallet.Currency == in.Currency {
			fail(w, 409, "wallet already exists for this owner and currency")
			return
		}
	}
	now := time.Now().UTC()
	wallet := billingWallet{ID: "wlt-" + randHex(8), Owner: in.Owner, Currency: in.Currency, Created: now, Updated: now}
	d.Wallets[wallet.ID] = wallet
	if err := d.save(); err != nil {
		delete(d.Wallets, wallet.ID)
		fail(w, 500, "could not save wallet")
		return
	}
	writeJSON(w, 200, wallet)
}
func (s *Server) creditBillingWallet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AmountCents int64  `json:"amount_cents"`
		Reason      string `json:"reason"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid credit payload")
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if !moneyOK(in.AmountCents) || in.AmountCents == 0 || in.Reason == "" || len(in.Reason) > 160 {
		fail(w, 400, "positive amount and reason are required")
		return
	}
	d := s.billing
	d.Lock()
	defer d.Unlock()
	wallet, ok := d.Wallets[r.PathValue("id")]
	if !ok {
		fail(w, 404, "wallet not found")
		return
	}
	now := time.Now().UTC()
	wallet.BalanceCents += in.AmountCents
	wallet.Updated = now
	credit := billingCredit{ID: "crd-" + randHex(8), WalletID: wallet.ID, Owner: wallet.Owner, Reason: in.Reason, Currency: wallet.Currency, AmountCents: in.AmountCents, Created: now}
	d.Wallets[wallet.ID] = wallet
	d.Credits[credit.ID] = credit
	if err := d.save(); err != nil {
		fail(w, 500, "could not credit wallet")
		return
	}
	writeJSON(w, 200, map[string]any{"wallet": wallet, "credit": credit})
}
func (s *Server) createBillingRefund(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InvoiceID    string `json:"invoice_id"`
		AmountCents  int64  `json:"amount_cents"`
		CreditWallet bool   `json:"credit_wallet"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid refund payload")
		return
	}
	in.InvoiceID = strings.TrimSpace(in.InvoiceID)
	d := s.billing
	d.Lock()
	defer d.Unlock()
	inv, ok := d.Invoices[in.InvoiceID]
	if !ok {
		fail(w, 404, "invoice not found")
		return
	}
	if !s.canAccessAccount(principalFrom(r), inv.Owner) {
		fail(w, 404, "invoice not found")
		return
	}
	if inv.Status != "paid" {
		fail(w, 409, "refunds require a paid invoice")
		return
	}
	refunded := int64(0)
	for _, item := range d.Refunds {
		if item.InvoiceID == inv.ID {
			refunded += item.AmountCents
		}
	}
	if in.AmountCents == 0 {
		in.AmountCents = inv.AmountCents + inv.TaxCents - refunded
	}
	if !moneyOK(in.AmountCents) || in.AmountCents == 0 || in.AmountCents+refunded > inv.AmountCents+inv.TaxCents {
		fail(w, 400, "refund exceeds the remaining paid amount")
		return
	}
	now := time.Now().UTC()
	item := billingRefund{ID: "rfd-" + randHex(8), InvoiceID: inv.ID, Owner: inv.Owner, Currency: inv.Currency, AmountCents: in.AmountCents, CreditWallet: in.CreditWallet, Created: now}
	d.Refunds[item.ID] = item
	var credit billingCredit
	if in.CreditWallet {
		wallet, found := billingWallet{}, false
		for _, existing := range d.Wallets {
			if existing.Owner == inv.Owner && existing.Currency == inv.Currency {
				wallet, found = existing, true
				break
			}
		}
		if !found {
			wallet = billingWallet{ID: "wlt-" + randHex(8), Owner: inv.Owner, Currency: inv.Currency, Created: now, Updated: now}
		}
		wallet.BalanceCents += in.AmountCents
		wallet.Updated = now
		credit = billingCredit{ID: "crd-" + randHex(8), WalletID: wallet.ID, Owner: wallet.Owner, Reason: "Refund " + inv.ID, Currency: wallet.Currency, AmountCents: in.AmountCents, Created: now}
		d.Wallets[wallet.ID] = wallet
		d.Credits[credit.ID] = credit
	}
	if err := d.save(); err != nil {
		delete(d.Refunds, item.ID)
		fail(w, 500, "could not save refund")
		return
	}
	writeJSON(w, 200, map[string]any{"refund": item, "credit": credit})
}
