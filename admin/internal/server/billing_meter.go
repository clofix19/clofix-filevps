package server

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type billingUsageSession struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	Kind        string     `json:"kind"`
	ResourceID  string     `json:"resource_id"`
	Name        string     `json:"name"`
	Owner       string     `json:"owner"`
	WorkerID    string     `json:"worker_id,omitempty"`
	WorkerName  string     `json:"worker_name,omitempty"`
	Product     string     `json:"product"`
	Tier        string     `json:"tier"`
	HourlyCents int64      `json:"hourly_cents"`
	Started     time.Time  `json:"started"`
	Ended       *time.Time `json:"ended,omitempty"`
	LastBilled  time.Time  `json:"last_billed"`
	Minutes     int64      `json:"minutes"`
	DueCents    int64      `json:"due_cents"`
	BilledCents int64      `json:"billed_cents"`
	Deleted     bool       `json:"deleted,omitempty"`
	Currency    string     `json:"currency"`
}

func usageSessionKey(kind, id string) string { return kind + "|" + id }

func usageRateNames(usage string) []string {
	switch usage {
	case "ssd":
		return []string{"ssd", "volume"}
	case "volume":
		return []string{"volume", "ssd"}
	case "backup":
		return []string{"backup", "ssd"}
	case "efs":
		return []string{"efs", "ssd"}
	case "s3":
		return []string{"s3", "ssd"}
	case "lb":
		return []string{"lb", "ipv4", "app"}
	case "cks":
		return []string{"cks"}
	case "app":
		return []string{"app"}
	default:
		return []string{usage}
	}
}

func inventoryHourly(catalog []billingResource, item billingInventoryItem, lowcost bool) int64 {
	total := int64(0)
	for _, line := range item.Lines {
		total += resourceChargeAt(catalog, line.Quantity, true, lowcost, usageRateNames(line.UsageType)...)
	}
	return total
}

func (s *Server) RunBillingMeter(ctx context.Context) {
	s.reconcileBillingMeter()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcileBillingMeter()
		}
	}
}

func (s *Server) reconcileBillingMeter() {
	s.meterBillingAt(time.Now().UTC(), s.scanBillingInventory())
}

func (s *Server) meterBillingAt(now time.Time, live []billingInventoryItem) {
	d := s.billing
	d.Lock()
	defer d.Unlock()
	if d.Usage == nil {
		d.Usage = map[string]billingUsageSession{}
	}
	lowcost := d.Currency.DefaultTier == "lowcost"
	tier := normalizeBillingTier(d.Currency.DefaultTier)
	currency := d.Currency.Currency
	if currency == "" {
		currency = "USD"
	}
	changed := false
	seen := map[string]bool{}
	for _, item := range live {
		key := usageSessionKey(item.Kind, item.ID)
		seen[key] = true
		hourly := inventoryHourly(d.Resources, item, lowcost)
		sess, ok := d.Usage[key]
		if !ok {
			d.Usage[key] = billingUsageSession{
				ID: "use-" + randHex(8), Key: key, Kind: item.Kind, ResourceID: item.ID, Name: item.Name,
				Owner: item.Owner, WorkerID: item.WorkerID, WorkerName: item.WorkerName, Product: item.Product,
				Tier: tier, HourlyCents: hourly, Started: now, LastBilled: now, Currency: currency,
			}
			changed = true
			continue
		}
		sess.Name, sess.Owner, sess.WorkerID, sess.WorkerName = item.Name, item.Owner, item.WorkerID, item.WorkerName
		sess.HourlyCents, sess.Tier, sess.Currency = hourly, tier, currency
		next := accrueUsage(sess, now, false)
		if next.DueCents != sess.DueCents || next.Minutes != sess.Minutes {
			changed = true
		}
		d.Usage[key] = next
	}
	for key, sess := range d.Usage {
		if seen[key] || sess.Ended != nil {
			continue
		}
		d.Usage[key] = accrueUsage(sess, now, true)
		changed = true
	}
	if changed {
		_ = d.save()
	}
}

func accrueUsage(sess billingUsageSession, now time.Time, closing bool) billingUsageSession {
	if sess.LastBilled.IsZero() {
		sess.LastBilled = sess.Started
	}
	if now.Before(sess.LastBilled) {
		now = sess.LastBilled
	}
	elapsed := now.Sub(sess.LastBilled)
	minutes := int64(elapsed / time.Minute)
	if closing && minutes == 0 && elapsed > 0 {
		minutes = 1
	}
	if minutes > 0 && sess.HourlyCents > 0 {
		add := (sess.HourlyCents*minutes + 30) / 60
		sess.DueCents += add
		sess.BilledCents += add
		sess.Minutes += minutes
		sess.LastBilled = sess.LastBilled.Add(time.Duration(minutes) * time.Minute)
	} else if minutes > 0 {
		sess.Minutes += minutes
		sess.LastBilled = sess.LastBilled.Add(time.Duration(minutes) * time.Minute)
	}
	if closing && sess.Ended == nil {
		end := now
		sess.Ended = &end
		sess.Deleted = true
	}
	return sess
}

func billingDueView(usage map[string]billingUsageSession, rate float64) map[string]any {
	sessions := make([]billingUsageSession, 0, len(usage))
	var due, usd, bdt int64
	for _, sess := range usage {
		if sess.DueCents <= 0 && sess.Ended != nil {
			continue
		}
		sessions = append(sessions, sess)
		due += sess.DueCents
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Owner == sessions[j].Owner {
			return sessions[i].Name < sessions[j].Name
		}
		return sessions[i].Owner < sessions[j].Owner
	})
	usd, bdt = convertUSDAndBDT(due, "USD", rate)
	return map[string]any{
		"sessions": sessions, "due_cents": due, "usd_cents": usd, "bdt_cents": bdt,
		"note": "Due bill is metered from hourly Standard or Low cost rates. Deleted resources stay on the due list until invoiced. A 5 or 10 minute run is billed as those minutes, not a full hour.",
	}
}

func takeDueUsage(usage map[string]billingUsageSession, owner string, rate float64, currency string) ([]billingInvoiceLine, []string, int64) {
	keys := []string{}
	for key, sess := range usage {
		if sess.DueCents <= 0 || (owner != "" && sess.Owner != owner) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []billingInvoiceLine{}
	amount := int64(0)
	for _, key := range keys {
		sess := usage[key]
		status := "running"
		if sess.Deleted {
			status = "deleted"
		}
		desc := fmt.Sprintf("%d min · %s · %s · %s", sess.Minutes, sess.Name, sess.Kind, status)
		qty := float64(sess.Minutes)
		if qty <= 0 {
			qty = 1
		}
		line := pricedInvoiceLine(sess.Product, sess.ResourceID, "hourly", desc, "minute", qty, sess.DueCents, currency, rate)
		line.ResourceName, line.WorkerID, line.WorkerName = sess.Name, sess.WorkerID, sess.WorkerName
		lines = append(lines, line)
		amount += sess.DueCents
		sess.DueCents = 0
		usage[key] = sess
	}
	return lines, keys, amount
}

func restoreDueUsage(usage map[string]billingUsageSession, keys []string, lines []billingInvoiceLine) {
	for i, key := range keys {
		sess, ok := usage[key]
		if !ok || i >= len(lines) {
			continue
		}
		sess.DueCents += lines[i].AmountCents
		usage[key] = sess
	}
}
