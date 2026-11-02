package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// platformStore holds white-label and marketplace SKU settings for BD reseller packaging.
type platformStore struct {
	sync.Mutex
	path string
	WhiteLabel struct {
		BrandName   string `json:"brand_name"`
		PanelDomain string `json:"panel_domain"`
		LogoURL     string `json:"logo_url"`
		PrimaryColor string `json:"primary_color"`
		SupportURL  string `json:"support_url"`
	} `json:"white_label"`
	Marketplace []marketplaceSKU `json:"marketplace"`
	WebConsole  struct {
		Enabled bool   `json:"enabled"`
		Note    string `json:"note"`
	} `json:"web_console"`
}

type marketplaceSKU struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Template    string `json:"template"` // managed-app template id or web-*
	BDTcents    int64  `json:"bdt_cents"`
	Enabled     bool   `json:"enabled"`
}

func loadPlatform(dir string) *platformStore {
	d := &platformStore{path: filepath.Join(dir, "platform.json")}
	b, err := os.ReadFile(d.path)
	if err == nil {
		_ = json.Unmarshal(b, d)
	}
	if d.Marketplace == nil {
		d.Marketplace = []marketplaceSKU{
			{ID: "sku-wordpress", Name: "WordPress", Description: "One-click WordPress Quick App", Template: "web-wordpress", BDTcents: 49900, Enabled: true},
			{ID: "sku-jenkins", Name: "Jenkins CI", Description: "Jenkins LTS Quick App", Template: "jenkins", BDTcents: 79900, Enabled: true},
			{ID: "sku-grafana", Name: "Grafana", Description: "Grafana dashboards Quick App", Template: "grafana", BDTcents: 59900, Enabled: true},
		}
	}
	if d.WhiteLabel.BrandName == "" {
		d.WhiteLabel.BrandName = "CloFix"
	}
	if d.WebConsole.Note == "" {
		d.WebConsole.Note = "In-browser SSH uses the existing VPS SSH helper and gateway ports. Full web terminal remains optional."
	}
	return d
}

func (d *platformStore) save() error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

func (s *Server) platformRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/platform", s.auth(s.getPlatform))
	m.HandleFunc("PUT /api/platform/white-label", s.auth(s.saveWhiteLabel))
	m.HandleFunc("PUT /api/platform/marketplace", s.auth(s.saveMarketplace))
	m.HandleFunc("PUT /api/platform/web-console", s.auth(s.saveWebConsole))
	m.HandleFunc("POST /api/floating-ips/attach", s.auth(s.attachFloatingIP))
	m.HandleFunc("GET /api/floating-ips", s.auth(s.listFloatingIPs))
}

func (s *Server) getPlatform(w http.ResponseWriter, r *http.Request) {
	d := s.platform
	d.Lock()
	defer d.Unlock()
	writeJSON(w, 200, d)
}

func (s *Server) saveWhiteLabel(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "white-label settings require administrator access")
		return
	}
	var in struct {
		BrandName    string `json:"brand_name"`
		PanelDomain  string `json:"panel_domain"`
		LogoURL      string `json:"logo_url"`
		PrimaryColor string `json:"primary_color"`
		SupportURL   string `json:"support_url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		fail(w, 400, "invalid white-label payload")
		return
	}
	d := s.platform
	d.Lock()
	defer d.Unlock()
	d.WhiteLabel.BrandName = strings.TrimSpace(in.BrandName)
	d.WhiteLabel.PanelDomain = strings.ToLower(strings.TrimSpace(in.PanelDomain))
	d.WhiteLabel.LogoURL = strings.TrimSpace(in.LogoURL)
	d.WhiteLabel.PrimaryColor = strings.TrimSpace(in.PrimaryColor)
	d.WhiteLabel.SupportURL = strings.TrimSpace(in.SupportURL)
	if d.WhiteLabel.BrandName == "" {
		d.WhiteLabel.BrandName = "CloFix"
	}
	if err := d.save(); err != nil {
		fail(w, 500, "could not save white-label settings")
		return
	}
	writeJSON(w, 200, d.WhiteLabel)
}

func (s *Server) saveMarketplace(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "marketplace settings require administrator access")
		return
	}
	var in struct {
		Items []marketplaceSKU `json:"items"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || len(in.Items) > 100 {
		fail(w, 400, "invalid marketplace payload")
		return
	}
	d := s.platform
	d.Lock()
	defer d.Unlock()
	d.Marketplace = in.Items
	if d.Marketplace == nil {
		d.Marketplace = []marketplaceSKU{}
	}
	if err := d.save(); err != nil {
		fail(w, 500, "could not save marketplace")
		return
	}
	writeJSON(w, 200, map[string]any{"items": d.Marketplace})
}

func (s *Server) saveWebConsole(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		fail(w, 403, "web console settings require administrator access")
		return
	}
	var in struct {
		Enabled bool   `json:"enabled"`
		Note    string `json:"note"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		fail(w, 400, "invalid web console payload")
		return
	}
	d := s.platform
	d.Lock()
	defer d.Unlock()
	d.WebConsole.Enabled = in.Enabled
	if strings.TrimSpace(in.Note) != "" {
		d.WebConsole.Note = strings.TrimSpace(in.Note)
	}
	if err := d.save(); err != nil {
		fail(w, 500, "could not save web console settings")
		return
	}
	writeJSON(w, 200, d.WebConsole)
}

func isAdmin(r *http.Request) bool {
	return principalFrom(r).Kind == "admin"
}
