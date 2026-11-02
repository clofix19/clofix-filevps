package server

import (
	"sort"
	"strings"

	"github.com/clofix/filevps/admin/internal/store"
	"github.com/clofix/filevps/worker/pkg/proto"
)

type billingInventoryItem struct {
	Kind        string               `json:"kind"`
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	WorkerID    string               `json:"worker_id,omitempty"`
	WorkerName  string               `json:"worker_name,omitempty"`
	Owner       string               `json:"owner,omitempty"`
	State       string               `json:"state"`
	Product     string               `json:"product"`
	StorageOnly bool                 `json:"storage_only,omitempty"`
	Note        string               `json:"note,omitempty"`
	Currency    string               `json:"currency"`
	AmountCents int64                `json:"amount_cents"`
	USDCents    int64                `json:"usd_cents"`
	BDTCents    int64                `json:"bdt_cents"`
	Lines       []billingInvoiceLine `json:"lines,omitempty"`
}

func billingGBFromMB(mb int64) float64 {
	if mb <= 0 {
		return 0
	}
	return float64(mb) / 1024
}
func billingGBFromBytes(n int64) float64 {
	if n <= 0 {
		return 0
	}
	g := float64(n) / (1024 * 1024 * 1024)
	if g < 0.01 {
		return 0.01
	}
	return g
}

func (s *Server) workerNames() map[string]string {
	out := map[string]string{}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		return out
	}
	for _, w := range workers {
		name := strings.TrimSpace(w.Hostname)
		if name == "" {
			name = w.ID
		}
		out[w.ID] = name
	}
	return out
}

func (s *Server) scanBillingInventory() []billingInventoryItem {
	d := s.billing
	d.Lock()
	catalog := append([]billingResource(nil), d.Resources...)
	currency := d.Currency.Currency
	if currency == "" {
		currency = "USD"
	}
	rate := billingFX(d.Currency)
	owners := map[string]string{}
	for _, sub := range d.Subscriptions {
		if sub.ResourceID != "" && sub.Owner != "" {
			owners[sub.ResourceID] = sub.Owner
		}
	}
	d.Unlock()
	workers := s.workerNames()
	items := []billingInventoryItem{}
	items = append(items, s.inventoryVPS(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryVolumes(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryBackups(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryDatabases(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryKubernetes(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryLoadBalancers(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryApps(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryEFS(catalog, currency, rate, owners, workers)...)
	items = append(items, s.inventoryS3(catalog, currency, rate, owners)...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return items[i].Name < items[j].Name
		}
		return items[i].Kind < items[j].Kind
	})
	return items
}

func inventoryLine(product, id, name, workerID, workerName, usage, desc, unit, currency string, qty float64, amount int64, rate float64) billingInvoiceLine {
	line := pricedInvoiceLine(product, id, usage, desc, unit, qty, amount, currency, rate)
	line.ResourceName, line.WorkerID, line.WorkerName = name, workerID, workerName
	return line
}

func finishInventoryItem(kind, product, id, name, workerID, workerName, owner, state, note, currency string, storageOnly bool, lines []billingInvoiceLine, rate float64) billingInventoryItem {
	amount := int64(0)
	for _, line := range lines {
		amount += line.AmountCents
	}
	usd, bdt := convertUSDAndBDT(amount, currency, rate)
	if owner == "" {
		owner = "platform"
	}
	return billingInventoryItem{
		Kind: kind, ID: id, Name: name, WorkerID: workerID, WorkerName: workerName, Owner: owner, State: state,
		Product: product, StorageOnly: storageOnly, Note: note, Currency: currency,
		AmountCents: amount, USDCents: usd, BDTCents: bdt, Lines: lines,
	}
}

func (s *Server) inventoryVPS(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	instances, err := s.Store.ListInstances()
	if err != nil {
		return nil
	}
	out := make([]billingInventoryItem, 0, len(instances))
	for _, inst := range instances {
		name := strings.TrimSpace(inst.Spec.Hostname)
		if name == "" {
			name = inst.ID
		}
		worker := workers[inst.WorkerID]
		owner := owners[inst.ID]
		if owner == "" {
			owner = inst.AccountID
		}
		stopped := inst.State == proto.StateStopped
		note := ""
		if stopped {
			note = "Stopped VPS bills storage only"
		}
		lines := []billingInvoiceLine{}
		if !stopped {
			cpuQty := inst.Spec.CPU
			if cpuQty <= 0 {
				cpuQty = 1
			}
			lines = append(lines, inventoryLine("vps", inst.ID, name, inst.WorkerID, worker, "cpu", "vCPU", "vCPU-month", currency, cpuQty, resourceCharge(catalog, cpuQty, "cpu"), rate))
			ramQty := billingGBFromMB(inst.Spec.RAMMB)
			if ramQty > 0 {
				lines = append(lines, inventoryLine("vps", inst.ID, name, inst.WorkerID, worker, "ram", "RAM", "GB-month", currency, ramQty, resourceCharge(catalog, ramQty, "ram"), rate))
			}
		}
		if diskQty := billingGBFromMB(inst.Spec.DiskMB); diskQty > 0 {
			lines = append(lines, inventoryLine("vps", inst.ID, name, inst.WorkerID, worker, "ssd", "Root disk", "GB-month", currency, diskQty, resourceCharge(catalog, diskQty, "ssd", "volume"), rate))
		}
		out = append(out, finishInventoryItem("vps", "vps", inst.ID, name, inst.WorkerID, worker, owner, inst.State, note, currency, stopped, lines, rate))
	}
	return out
}

func (s *Server) inventoryVolumes(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	volumes, err := s.Store.ListVolumes()
	if err != nil {
		return nil
	}
	out := make([]billingInventoryItem, 0, len(volumes))
	for _, vol := range volumes {
		if vol.State == "DELETING" {
			continue
		}
		worker := workers[vol.WorkerID]
		owner := owners[vol.ID]
		if owner == "" {
			owner = owners[vol.InstanceID]
		}
		if owner == "" {
			owner = vol.AccountID
		}
		name := vol.Name
		if name == "" {
			name = vol.ID
		}
		qty := billingGBFromMB(vol.LimitMB)
		if qty == 0 {
			qty = billingGBFromBytes(int64(vol.UsageBytes))
		}
		if qty == 0 {
			qty = 1
		}
		lines := []billingInvoiceLine{inventoryLine("volume", vol.ID, name, vol.WorkerID, worker, "volume", "Block volume", "GB-month", currency, qty, resourceCharge(catalog, qty, "volume", "ssd"), rate)}
		out = append(out, finishInventoryItem("volume", "volume", vol.ID, name, vol.WorkerID, worker, owner, vol.State, "", currency, true, lines, rate))
	}
	return out
}

func (s *Server) inventoryBackups(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	s.usageMu.RLock()
	backups := make([]proto.BackupInfo, 0, len(s.backups))
	for _, b := range s.backups {
		backups = append(backups, b)
	}
	s.usageMu.RUnlock()
	out := make([]billingInventoryItem, 0, len(backups))
	for _, b := range backups {
		if b.State == "DELETING" {
			continue
		}
		worker := workers[b.WorkerID]
		owner := owners[b.ID]
		if owner == "" {
			owner = owners[b.InstanceID]
		}
		qty := billingGBFromBytes(int64(b.SizeBytes))
		if qty == 0 {
			qty = billingGBFromMB(b.DiskMB)
		}
		if qty == 0 {
			qty = 1
		}
		name := b.ID
		if b.InstanceID != "" {
			name = b.InstanceID + " backup"
		}
		lines := []billingInvoiceLine{inventoryLine("backup", b.ID, name, b.WorkerID, worker, "backup", "VPS backup", "GB-month", currency, qty, resourceCharge(catalog, qty, "backup", "ssd"), rate)}
		out = append(out, finishInventoryItem("backup", "backup", b.ID, name, b.WorkerID, worker, owner, b.State, "", currency, true, lines, rate))
	}
	return out
}

func (s *Server) inventoryDatabases(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	if s.databases == nil {
		return nil
	}
	s.databases.Lock()
	projects := make([]struct {
		ID, Name, Owner, WorkerID, State string
		CPU                              float64
		RAMMB, DiskMB                    int64
	}, 0, len(s.databases.Projects))
	for _, p := range s.databases.Projects {
		projects = append(projects, struct {
			ID, Name, Owner, WorkerID, State string
			CPU                              float64
			RAMMB, DiskMB                    int64
		}{p.Spec.ID, p.Name, p.Owner, p.WorkerID, p.State, p.Spec.CPU, p.Spec.RAMMB, p.Spec.DiskMB})
	}
	dbBackups := make([]struct {
		ID, Name, Owner, WorkerID, State, ProjectID string
		Bytes                                       int64
	}, 0, len(s.databases.Backups))
	for _, b := range s.databases.Backups {
		if b.State == "DELETING" {
			continue
		}
		bytes := b.Bytes
		if bytes <= 0 {
			bytes = b.PlainBytes
		}
		if bytes <= 0 {
			bytes = b.ReservedBytes
		}
		name := b.Database
		if name == "" {
			name = b.ID
		}
		dbBackups = append(dbBackups, struct {
			ID, Name, Owner, WorkerID, State, ProjectID string
			Bytes                                       int64
		}{b.ID, name + " backup", b.Owner, b.WorkerID, b.State, b.ProjectID, bytes})
	}
	s.databases.Unlock()
	out := []billingInventoryItem{}
	for _, p := range projects {
		name := p.Name
		if name == "" {
			name = p.ID
		}
		worker := workers[p.WorkerID]
		owner := p.Owner
		if owner == "" {
			owner = owners[p.ID]
		}
		stopped := strings.EqualFold(p.State, proto.StateStopped)
		note := ""
		if stopped {
			note = "Stopped database bills storage only"
		}
		lines := []billingInvoiceLine{}
		if !stopped {
			cpuQty := p.CPU
			if cpuQty <= 0 {
				cpuQty = 1
			}
			lines = append(lines, inventoryLine("database", p.ID, name, p.WorkerID, worker, "cpu", "Database vCPU", "vCPU-month", currency, cpuQty, resourceCharge(catalog, cpuQty, "cpu"), rate))
			if ramQty := billingGBFromMB(p.RAMMB); ramQty > 0 {
				lines = append(lines, inventoryLine("database", p.ID, name, p.WorkerID, worker, "ram", "Database RAM", "GB-month", currency, ramQty, resourceCharge(catalog, ramQty, "ram"), rate))
			}
		}
		if diskQty := billingGBFromMB(p.DiskMB); diskQty > 0 {
			lines = append(lines, inventoryLine("database", p.ID, name, p.WorkerID, worker, "ssd", "Database disk", "GB-month", currency, diskQty, resourceCharge(catalog, diskQty, "ssd", "volume"), rate))
		}
		out = append(out, finishInventoryItem("database", "database", p.ID, name, p.WorkerID, worker, owner, p.State, note, currency, stopped, lines, rate))
	}
	for _, b := range dbBackups {
		worker := workers[b.WorkerID]
		qty := billingGBFromBytes(b.Bytes)
		if qty == 0 {
			qty = 1
		}
		lines := []billingInvoiceLine{inventoryLine("backup", b.ID, b.Name, b.WorkerID, worker, "backup", "Database backup", "GB-month", currency, qty, resourceCharge(catalog, qty, "backup", "ssd"), rate)}
		out = append(out, finishInventoryItem("backup", "backup", b.ID, b.Name, b.WorkerID, worker, b.Owner, b.State, "", currency, true, lines, rate))
	}
	return out
}

func (s *Server) inventoryKubernetes(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	if s.kubernetes == nil {
		return nil
	}
	s.kubernetes.Lock()
	clusters := make([]struct {
		ID, Name, Owner, WorkerID, State string
		CPU                              float64
		RAMMB, DiskMB                    int64
		Nodes                            int
		HA                               bool
	}, 0, len(s.kubernetes.Clusters))
	type k8sBackup struct {
		ID, Name, Owner, WorkerID, State string
		Bytes                            int64
	}
	k8sBackups := []k8sBackup{}
	for _, c := range s.kubernetes.Clusters {
		if c.State == "DELETED" || c.State == "DELETING" {
			continue
		}
		var cpu float64
		var ram, disk int64
		nodes := 0
		for _, node := range c.Nodes {
			if node.State == "DELETED" {
				continue
			}
			nodes++
			pool, ok := c.Pool(node.PoolID)
			if !ok {
				pool = c.NodePool
			}
			cpu += pool.CPU
			ram += pool.RAMMB
			disk += pool.DiskMB
		}
		if nodes == 0 {
			for _, pool := range c.Pools() {
				n := pool.Count
				if n < 1 {
					n = 1
				}
				nodes += n
				cpu += pool.CPU * float64(n)
				ram += pool.RAMMB * int64(n)
				disk += pool.DiskMB * int64(n)
			}
		}
		if nodes < 1 {
			nodes = 1
			cpu = c.NodePool.CPU
			ram = c.NodePool.RAMMB
			disk = c.NodePool.DiskMB
		}
		clusters = append(clusters, struct {
			ID, Name, Owner, WorkerID, State string
			CPU                              float64
			RAMMB, DiskMB                    int64
			Nodes                            int
			HA                               bool
		}{c.ID, c.Name, c.Owner, c.ControlPlaneWorkerID, c.State, cpu, ram, disk, nodes, c.HA})
		for _, b := range c.Backups {
			if b.State == "FAILED" {
				continue
			}
			k8sBackups = append(k8sBackups, k8sBackup{ID: b.ID, Name: c.Name + " etcd backup", Owner: c.Owner, WorkerID: c.ControlPlaneWorkerID, State: b.State, Bytes: b.SizeBytes})
		}
	}
	s.kubernetes.Unlock()
	out := []billingInventoryItem{}
	for _, c := range clusters {
		name := c.Name
		if name == "" {
			name = c.ID
		}
		worker := workers[c.WorkerID]
		owner := c.Owner
		if owner == "" {
			owner = owners[c.ID]
		}
		lines := []billingInvoiceLine{inventoryLine("cks", c.ID, name, c.WorkerID, worker, "cks", "CKS control plane", "CKS-month", currency, 1, resourceCharge(catalog, 1, "cks"), rate)}
		cpuQty := c.CPU
		if cpuQty <= 0 {
			cpuQty = 1
		}
		lines = append(lines, inventoryLine("cks", c.ID, name, c.WorkerID, worker, "cpu", "CKS node vCPU", "vCPU-month", currency, cpuQty, resourceCharge(catalog, cpuQty, "cpu"), rate))
		if ramQty := billingGBFromMB(c.RAMMB); ramQty > 0 {
			lines = append(lines, inventoryLine("cks", c.ID, name, c.WorkerID, worker, "ram", "CKS node RAM", "GB-month", currency, ramQty, resourceCharge(catalog, ramQty, "ram"), rate))
		}
		if diskQty := billingGBFromMB(c.DiskMB); diskQty > 0 {
			lines = append(lines, inventoryLine("cks", c.ID, name, c.WorkerID, worker, "ssd", "CKS node disk", "GB-month", currency, diskQty, resourceCharge(catalog, diskQty, "ssd", "volume"), rate))
		}
		note := "CKS control plane plus node compute. Attached CloFix load balancers bill as Load Balancer."
		if c.HA {
			note = "HA " + note
		}
		out = append(out, finishInventoryItem("cks", "cks", c.ID, name, c.WorkerID, worker, owner, c.State, note, currency, false, lines, rate))
	}
	for _, b := range k8sBackups {
		worker := workers[b.WorkerID]
		qty := billingGBFromBytes(b.Bytes)
		if qty == 0 {
			qty = 1
		}
		lines := []billingInvoiceLine{inventoryLine("backup", b.ID, b.Name, b.WorkerID, worker, "backup", "CKS etcd backup", "GB-month", currency, qty, resourceCharge(catalog, qty, "backup", "ssd"), rate)}
		out = append(out, finishInventoryItem("backup", "backup", b.ID, b.Name, b.WorkerID, worker, b.Owner, b.State, "", currency, true, lines, rate))
	}
	return out
}

func (s *Server) inventoryLoadBalancers(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	if s.balancers == nil {
		return nil
	}
	s.balancers.Lock()
	type snap struct {
		ID, Name string
		Deleting bool
		Members  []string
	}
	list := make([]snap, 0, len(s.balancers.Items))
	for _, b := range s.balancers.Items {
		if b == nil || b.Deleting {
			continue
		}
		list = append(list, snap{ID: b.ID, Name: b.Name, Members: append([]string(nil), b.Instances...)})
	}
	s.balancers.Unlock()
	instances, _ := s.Store.ListInstances()
	byID := map[string]store.Instance{}
	for _, inst := range instances {
		byID[inst.ID] = inst
	}
	out := make([]billingInventoryItem, 0, len(list))
	for _, b := range list {
		name := b.Name
		if name == "" {
			name = b.ID
		}
		workerID, workerName := "", ""
		seen := map[string]bool{}
		names := []string{}
		for _, id := range b.Members {
			inst, ok := byID[id]
			if !ok || seen[inst.WorkerID] {
				continue
			}
			seen[inst.WorkerID] = true
			if workerID == "" {
				workerID = inst.WorkerID
				workerName = workers[inst.WorkerID]
			}
			if label := workers[inst.WorkerID]; label != "" {
				names = append(names, label)
			}
		}
		if len(names) > 1 {
			workerName = strings.Join(names, ", ")
		}
		owner := owners[b.ID]
		lines := []billingInvoiceLine{inventoryLine("load-balancer", b.ID, name, workerID, workerName, "lb", "Load balancer", "balancer-month", currency, 1, resourceCharge(catalog, 1, "lb", "ipv4", "app"), rate)}
		out = append(out, finishInventoryItem("load-balancer", "load-balancer", b.ID, name, workerID, workerName, owner, "ACTIVE", "", currency, false, lines, rate))
	}
	return out
}

func (s *Server) inventoryApps(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	if s.applications == nil {
		return nil
	}
	s.applications.Lock()
	managed := make([]managedApp, 0, len(s.applications.Managed))
	for _, a := range s.applications.Managed {
		managed = append(managed, a)
	}
	apps := make([]application, 0, len(s.applications.Apps))
	for _, a := range s.applications.Apps {
		apps = append(apps, a)
	}
	s.applications.Unlock()
	out := []billingInventoryItem{}
	for _, a := range managed {
		name := a.Name
		if name == "" {
			name = a.ID
		}
		worker := workers[a.WorkerID]
		owner := a.Owner
		if owner == "" {
			owner = owners[a.ID]
		}
		stopped := strings.EqualFold(a.Desired, "stopped") || a.State == proto.StateStopped
		note := ""
		lines := []billingInvoiceLine{}
		if stopped {
			note = "Stopped application has no compute charge"
		} else {
			cpuQty := a.Spec.CPU
			if cpuQty <= 0 {
				cpuQty = 1
			}
			if c := resourceCharge(catalog, cpuQty, "cpu"); c > 0 {
				lines = append(lines, inventoryLine("application", a.ID, name, a.WorkerID, worker, "cpu", "App vCPU", "vCPU-month", currency, cpuQty, c, rate))
			}
			if ramQty := billingGBFromMB(a.Spec.RAMMB); ramQty > 0 {
				if c := resourceCharge(catalog, ramQty, "ram"); c > 0 {
					lines = append(lines, inventoryLine("application", a.ID, name, a.WorkerID, worker, "ram", "App RAM", "GB-month", currency, ramQty, c, rate))
				}
			}
			if len(lines) == 0 {
				lines = append(lines, inventoryLine("application", a.ID, name, a.WorkerID, worker, "app", "Deployed application", "app-month", currency, 1, resourceCharge(catalog, 1, "app"), rate))
			}
		}
		state := a.State
		if state == "" {
			state = a.Desired
		}
		out = append(out, finishInventoryItem("application", "application", a.ID, name, a.WorkerID, worker, owner, state, note, currency, stopped, lines, rate))
	}
	for _, a := range apps {
		name := a.Name
		if name == "" {
			name = a.ID
		}
		owner := a.Owner
		if owner == "" {
			owner = owners[a.ID]
		}
		lines := []billingInvoiceLine{inventoryLine("application", a.ID, name, "", "", "app", "Application", "app-month", currency, 1, resourceCharge(catalog, 1, "app"), rate)}
		out = append(out, finishInventoryItem("application", "application", a.ID, name, "", "", owner, "ACTIVE", "", currency, false, lines, rate))
	}
	return out
}

func (s *Server) inventoryEFS(catalog []billingResource, currency string, rate float64, owners, workers map[string]string) []billingInventoryItem {
	if s.efs == nil {
		return nil
	}
	s.efs.Lock()
	shares := make([]efsShare, 0, len(s.efs.Shares))
	for _, share := range s.efs.Shares {
		shares = append(shares, share)
	}
	s.efs.Unlock()
	instances, _ := s.Store.ListInstances()
	byID := map[string]store.Instance{}
	for _, inst := range instances {
		byID[inst.ID] = inst
	}
	out := make([]billingInventoryItem, 0, len(shares))
	for _, share := range shares {
		name := share.Name
		if name == "" {
			name = share.ID
		}
		attached := make([]string, 0, len(share.Attachments))
		for _, att := range share.Attachments {
			attached = append(attached, att.InstanceID)
		}
		if s.balancers != nil {
			s.balancers.Lock()
			for _, b := range s.balancers.Items {
				if b != nil && b.EFSID == share.ID {
					attached = append(attached, b.Instances...)
				}
			}
			s.balancers.Unlock()
		}
		workerID, workerName := "", ""
		seen := map[string]bool{}
		names := []string{}
		for _, id := range attached {
			inst, ok := byID[id]
			if !ok || seen[inst.WorkerID] {
				continue
			}
			seen[inst.WorkerID] = true
			if workerID == "" {
				workerID = inst.WorkerID
				workerName = workers[inst.WorkerID]
			}
			if label := workers[inst.WorkerID]; label != "" {
				names = append(names, label)
			}
		}
		if len(names) > 1 {
			workerName = strings.Join(names, ", ")
		}
		owner := owners[share.ID]
		lines := []billingInvoiceLine{inventoryLine("efs", share.ID, name, workerID, workerName, "efs", "EFS share", "GB-month", currency, 1, resourceCharge(catalog, 1, "efs", "ssd"), rate)}
		out = append(out, finishInventoryItem("efs", "efs", share.ID, name, workerID, workerName, owner, "ACTIVE", "", currency, true, lines, rate))
	}
	return out
}

func (s *Server) inventoryS3(catalog []billingResource, currency string, rate float64, owners map[string]string) []billingInventoryItem {
	if s.S3 == nil {
		return nil
	}
	conns, err := s.S3.ListConnections()
	if err != nil {
		return nil
	}
	out := make([]billingInventoryItem, 0, len(conns))
	for _, c := range conns {
		name := c.Name
		if name == "" {
			name = c.Bucket
		}
		if name == "" {
			name = c.ID
		}
		owner := owners[c.ID]
		lines := []billingInvoiceLine{inventoryLine("object-storage", c.ID, name, "", "", "s3", "Object storage bucket", "GB-month", currency, 1, resourceCharge(catalog, 1, "s3", "ssd"), rate)}
		out = append(out, finishInventoryItem("s3", "object-storage", c.ID, name, "", "", owner, "ACTIVE", "", currency, true, lines, rate))
	}
	return out
}
