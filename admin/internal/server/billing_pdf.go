package server

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Server) downloadBillingInvoicePDF(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !strings.HasPrefix(id, "inv-") || len(id) > 40 {
		fail(w, 400, "invalid invoice id")
		return
	}
	d := s.billing
	d.Lock()
	inv, ok := d.Invoices[id]
	cur := d.Currency
	rate := billingFX(d.Currency)
	refunded := int64(0)
	for _, item := range d.Refunds {
		if item.InvoiceID == inv.ID {
			refunded += item.AmountCents
		}
	}
	d.Unlock()
	if !ok || !s.canAccessAccount(principalFrom(r), inv.Owner) {
		fail(w, 404, "invoice not found")
		return
	}
	brand := invoiceBrandFrom(cur, s.UIDir, os.Getenv("CLOFIX_DATA_DIR"))
	inv = enrichInvoice(inv, rate)
	if inv.TaxCents == 0 && brand.TaxPercent > 0 {
		inv.TaxCents = invoiceTaxCents(inv.AmountCents, brand.TaxPercent)
	}
	inv.USDCents, inv.BDTCents = 0, 0
	stampInvoiceMoney(&inv, rate)
	d.Lock()
	if stored, ok := d.Invoices[id]; ok {
		changed := false
		if stored.TaxCents == 0 && inv.TaxCents > 0 {
			stored.TaxCents = inv.TaxCents
			changed = true
		}
		if stored.USDCents != inv.USDCents || stored.BDTCents != inv.BDTCents || stored.FXBDTPerUSD != inv.FXBDTPerUSD {
			stored.USDCents, stored.BDTCents, stored.FXBDTPerUSD = inv.USDCents, inv.BDTCents, inv.FXBDTPerUSD
			changed = true
		}
		if changed {
			d.Invoices[id] = stored
			_ = d.save()
		}
	}
	d.Unlock()
	raw := invoicePDF(inv, brand, refunded)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(raw)
}

type invoiceBrand struct {
	Company    string
	From       string
	Email      string
	Footer     string
	Notes      []string
	TaxName    string
	TaxPercent float64
	Logo       *invoiceLogo
}

func invoiceTaxCents(amountCents int64, taxPercent float64) int64 {
	if amountCents <= 0 || taxPercent <= 0 {
		return 0
	}
	return int64(float64(amountCents)*taxPercent/100 + 0.5)
}

func invoiceBrandFrom(cur billingCurrency, uiDir, dataDir string) invoiceBrand {
	taxName := strings.TrimSpace(cur.TaxName)
	if taxName == "" {
		taxName = "VAT"
	}
	taxPercent := cur.TaxPercent
	if taxPercent < 0 {
		taxPercent = 0
	}
	company := strings.TrimSpace(cur.InvoiceCompany)
	if company == "" {
		company = "CloFix Cloud"
	}
	from := strings.TrimSpace(cur.InvoiceFrom)
	if from == "" {
		from = "CloFix Infotech"
	}
	email := strings.TrimSpace(cur.InvoiceEmail)
	if email == "" {
		email = "billing@clofix.cloud"
	}
	footer := strings.TrimSpace(cur.InvoiceFooter)
	if footer == "" {
		footer = "CloFix Cloud - Standard invoice - Generated from Billing & Payments"
	}
	notes := []string{}
	for _, line := range strings.Split(cur.InvoiceNotes, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			notes = append(notes, line)
		}
	}
	if len(notes) == 0 {
		notes = []string{
			"Thank you for your business. This is a standard CloFix Cloud invoice.",
			"Amounts are shown in invoice currency with USD/BDT equivalents.",
		}
	}
	logoName := filepath.Base(strings.TrimSpace(cur.InvoiceLogo))
	if logoName == "." || logoName == ".." {
		logoName = ""
	}
	return invoiceBrand{
		Company: company, From: from, Email: email, Footer: footer, Notes: notes,
		TaxName: taxName, TaxPercent: taxPercent,
		Logo: loadInvoiceLogo(uiDir, dataDir, logoName),
	}
}

type invoiceLogo struct {
	Width  int
	Height int
	RGB    []byte
}

func loadInvoiceLogo(uiDir, dataDir, preferred string) *invoiceLogo {
	candidates := []string{}
	if preferred != "" {
		name := filepath.Base(preferred)
		if dataDir != "" {
			candidates = append(candidates, filepath.Join(dataDir, "branding", name), filepath.Join(dataDir, name))
		}
		if uiDir != "" {
			candidates = append(candidates, filepath.Join(uiDir, name), filepath.Join(uiDir, "branding", name))
		}
	}
	if dataDir != "" {
		candidates = append(candidates, filepath.Join(dataDir, "branding", "invoice-logo.png"), filepath.Join(dataDir, "invoice-logo.png"))
	}
	if uiDir != "" {
		candidates = append(candidates, filepath.Join(uiDir, "invoice-logo.png"), filepath.Join(uiDir, "branding", "invoice-logo.png"), filepath.Join(uiDir, "clofix-logo.png"))
	}
	seen := map[string]bool{}
	for _, path := range candidates {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if logo := decodeInvoiceLogo(path); logo != nil {
			return logo
		}
	}
	return nil
}

func decodeInvoiceLogo(path string) *invoiceLogo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 8 || h < 8 || w > 2000 || h > 2000 {
		return nil
	}
	// Composite onto the dark header so transparent logos do not leave a white box.
	const bgR, bgG, bgB = 15.0, 26.0, 41.0
	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			alpha := float64(a) / 65535
			i := (y*w + x) * 3
			rgb[i] = byte(float64(r>>8)*alpha + bgR*(1-alpha))
			rgb[i+1] = byte(float64(g>>8)*alpha + bgG*(1-alpha))
			rgb[i+2] = byte(float64(b>>8)*alpha + bgB*(1-alpha))
		}
	}
	return &invoiceLogo{Width: w, Height: h, RGB: rgb}
}

// pdfLogoPlacement fits the logo into maxW x maxH while preserving aspect ratio.
func pdfLogoPlacement(logo *invoiceLogo, maxW, maxH, x, yBottom float64) (drawW, drawH, drawX, drawY float64) {
	if logo == nil || logo.Width <= 0 || logo.Height <= 0 {
		return 0, 0, x, yBottom
	}
	aw, ah := float64(logo.Width), float64(logo.Height)
	scale := maxW / aw
	if ah*scale > maxH {
		scale = maxH / ah
	}
	drawW, drawH = aw*scale, ah*scale
	drawX = x
	drawY = yBottom + (maxH-drawH)/2
	return drawW, drawH, drawX, drawY
}

func invoiceStatusPDF(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "paid":
		return "PAID"
	case "void", "cancelled", "canceled":
		return "VOID"
	default:
		return "UNPAID"
	}
}

func invoicePDF(inv billingInvoice, brand invoiceBrand, refunded int64) []byte {
	if brand.TaxPercent > 0 && inv.TaxCents == 0 {
		inv.TaxCents = invoiceTaxCents(inv.AmountCents, brand.TaxPercent)
		inv.USDCents, inv.BDTCents = 0, 0
	}
	stampInvoiceMoney(&inv, inv.FXBDTPerUSD)
	total := inv.AmountCents + inv.TaxCents
	issued := inv.Created.UTC().Format("02 Jan 2006")
	status := invoiceStatusPDF(inv.Status)
	desc := strings.TrimSpace(inv.Description)
	if desc == "" {
		desc = "CloFix Cloud service"
	}
	if brand.Company == "" {
		brand.Company = "CloFix Cloud"
	}
	if brand.From == "" {
		brand.From = "CloFix Infotech"
	}
	if brand.Email == "" {
		brand.Email = "billing@clofix.cloud"
	}
	if brand.Footer == "" {
		brand.Footer = "CloFix Cloud - Standard invoice - Generated from Billing & Payments"
	}
	if brand.TaxName == "" {
		brand.TaxName = "VAT"
	}
	taxLabel := brand.TaxName
	if brand.TaxPercent > 0 {
		if brand.TaxPercent == float64(int64(brand.TaxPercent)) {
			taxLabel = fmt.Sprintf("%s %d%%", brand.TaxName, int64(brand.TaxPercent))
		} else {
			taxLabel = fmt.Sprintf("%s %.2f%%", brand.TaxName, brand.TaxPercent)
		}
	}
	if len(brand.Notes) == 0 {
		brand.Notes = []string{"Thank you for your business."}
	}
	logo := brand.Logo
	var body strings.Builder
	// White page.
	body.WriteString("1 1 1 rg\n0 0 612 792 re f\n")
	// Header band + teal accent.
	body.WriteString("0.06 0.10 0.16 rg\n0 730 612 62 re f\n")
	body.WriteString("0.18 0.62 0.56 rg\n0 726 612 4 re f\n")
	if logo != nil {
		dw, dh, dx, dy := pdfLogoPlacement(logo, 150, 44, 44, 738)
		body.WriteString(fmt.Sprintf("q\n%.2f 0 0 %.2f %.2f %.2f cm\n/Im1 Do\nQ\n", dw, dh, dx, dy))
	} else {
		body.WriteString(pdfText("F2", 18, 44, 756, 1, 1, 1, brand.Company))
	}
	body.WriteString(pdfRight("F2", 20, 572, 762, 1, 1, 1, "INVOICE"))
	body.WriteString(pdfRight("F1", 9, 572, 744, 0.70, 0.90, 0.84, status))

	// Meta strip.
	body.WriteString("0.96 0.97 0.98 rg\n40 668 532 42 re f\n")
	body.WriteString(pdfText("F2", 7, 52, 698, 0.45, 0.50, 0.55, "INVOICE NUMBER"))
	body.WriteString(pdfText("F1", 10, 52, 682, 0.08, 0.10, 0.14, inv.ID))
	body.WriteString(pdfText("F2", 7, 230, 698, 0.45, 0.50, 0.55, "DATE"))
	body.WriteString(pdfText("F1", 10, 230, 682, 0.08, 0.10, 0.14, issued))
	body.WriteString(pdfText("F2", 7, 360, 698, 0.45, 0.50, 0.55, "STATUS"))
	body.WriteString(pdfText("F1", 10, 360, 682, 0.08, 0.10, 0.14, status))
	if !inv.PeriodStart.IsZero() {
		body.WriteString(pdfText("F2", 7, 460, 698, 0.45, 0.50, 0.55, "SERVICE PERIOD"))
		body.WriteString(pdfText("F1", 9, 460, 682, 0.08, 0.10, 0.14, inv.PeriodStart.UTC().Format("02 Jan")+" - "+inv.PeriodEnd.UTC().Format("02 Jan 2006")))
	}

	// Parties.
	body.WriteString(pdfText("F2", 8, 52, 648, 0.45, 0.50, 0.55, "BILL TO"))
	body.WriteString(pdfText("F2", 8, 320, 648, 0.45, 0.50, 0.55, "FROM"))
	body.WriteString("0.86 0.89 0.91 RG\n0.8 w\n40 638 532 0 re S\n")
	body.WriteString(pdfText("F2", 12, 52, 622, 0.08, 0.10, 0.14, inv.Owner))
	billSub := desc
	if inv.SubscriptionID != "" {
		billSub = "Subscription " + inv.SubscriptionID
	}
	for i, line := range pdfWrap(billSub, 38) {
		if i > 1 {
			break
		}
		body.WriteString(pdfText("F1", 9, 52, 606-float64(i*12), 0.35, 0.40, 0.45, line))
	}
	body.WriteString(pdfText("F2", 12, 320, 622, 0.08, 0.10, 0.14, brand.From))
	body.WriteString(pdfText("F1", 9, 320, 606, 0.35, 0.40, 0.45, brand.Company))
	body.WriteString(pdfText("F1", 9, 320, 592, 0.35, 0.40, 0.45, brand.Email))

	// Line-item table.
	y := 556.0
	body.WriteString(fmt.Sprintf("0.06 0.10 0.16 rg\n40 %.1f 532 24 re f\n", y-8))
	body.WriteString(pdfText("F2", 8, 50, y, 1, 1, 1, "#"))
	body.WriteString(pdfText("F2", 8, 68, y, 1, 1, 1, "DESCRIPTION"))
	body.WriteString(pdfText("F2", 8, 278, y, 1, 1, 1, "RESOURCE"))
	body.WriteString(pdfRight("F2", 8, 382, y, 1, 1, 1, "QTY"))
	body.WriteString(pdfRight("F2", 8, 462, y, 1, 1, 1, "UNIT"))
	body.WriteString(pdfRight("F2", 8, 560, y, 1, 1, 1, "AMOUNT"))
	y -= 28

	lines := inv.Lines
	if len(lines) == 0 {
		item := desc
		if inv.SubscriptionID != "" {
			item = desc + " / " + inv.SubscriptionID
		}
		lines = []billingInvoiceLine{{
			Description: item, ResourceID: inv.ResourceID, Quantity: 1, Unit: "ea",
			UnitCents: inv.AmountCents, AmountCents: inv.AmountCents, USDCents: inv.USDCents, BDTCents: inv.BDTCents,
		}}
	}
	if len(lines) > 10 {
		lines = lines[:10]
	}
	for i, line := range lines {
		if i%2 == 1 {
			body.WriteString(fmt.Sprintf("0.97 0.98 0.99 rg\n40 %.1f 532 22 re f\n", y-8))
		}
		label := strings.TrimSpace(line.Description)
		if label == "" {
			label = desc
		}
		wrapped := pdfWrap(label, 30)
		body.WriteString(pdfText("F1", 9, 50, y, 0.08, 0.10, 0.14, fmt.Sprintf("%d", i+1)))
		body.WriteString(pdfText("F1", 9, 68, y, 0.08, 0.10, 0.14, wrapped[0]))
		resource := line.ResourceID
		if resource == "" {
			resource = line.UsageType
		}
		if resource == "" {
			resource = "-"
		}
		qty := line.Quantity
		if qty <= 0 {
			qty = 1
		}
		unitLabel := line.Unit
		if unitLabel == "" {
			unitLabel = "ea"
		}
		unitPrice := line.UnitCents
		if unitPrice == 0 && qty > 0 {
			unitPrice = line.AmountCents
		}
		body.WriteString(pdfText("F1", 8, 278, y, 0.08, 0.10, 0.14, resource))
		body.WriteString(pdfRight("F1", 9, 382, y, 0.08, 0.10, 0.14, fmt.Sprintf("%.2g %s", qty, unitLabel)))
		body.WriteString(pdfRight("F1", 9, 462, y, 0.08, 0.10, 0.14, pdfMoney(unitPrice, inv.Currency)))
		body.WriteString(pdfRight("F1", 9, 560, y, 0.08, 0.10, 0.14, pdfMoney(line.AmountCents, inv.Currency)))
		y -= 16
		for _, extra := range wrapped[1:] {
			body.WriteString(pdfText("F1", 8, 68, y, 0.40, 0.45, 0.50, extra))
			y -= 12
		}
		y -= 6
	}

	// Totals.
	y -= 6
	body.WriteString(fmt.Sprintf("0.86 0.89 0.91 RG\n0.7 w\n310 %.1f 262 0 re S\n", y+12))
	body.WriteString(pdfText("F1", 10, 324, y, 0.35, 0.40, 0.45, "Subtotal"))
	body.WriteString(pdfRight("F1", 10, 560, y, 0.08, 0.10, 0.14, pdfMoney(inv.AmountCents, inv.Currency)))
	y -= 18
	body.WriteString(pdfText("F1", 10, 324, y, 0.35, 0.40, 0.45, taxLabel))
	body.WriteString(pdfRight("F1", 10, 560, y, 0.08, 0.10, 0.14, pdfMoney(inv.TaxCents, inv.Currency)))
	y -= 28
	body.WriteString(fmt.Sprintf("0.06 0.10 0.16 rg\n310 %.1f 262 34 re f\n", y-12))
	body.WriteString(pdfText("F2", 11, 322, y, 1, 1, 1, "Total due"))
	body.WriteString(pdfRight("F2", 13, 560, y, 1, 1, 1, pdfMoney(total, inv.Currency)))
	y -= 30
	body.WriteString(pdfRight("F1", 8, 560, y, 0.40, 0.45, 0.50, pdfMoney(inv.USDCents, "USD")+" / "+pdfMoney(inv.BDTCents, "BDT")))
	y -= 24

	if inv.Paid != nil {
		body.WriteString(pdfText("F1", 10, 52, y, 0.08, 0.10, 0.14, "Paid on "+inv.Paid.UTC().Format("02 Jan 2006 15:04 UTC")))
		y -= 16
	}
	if refunded > 0 {
		body.WriteString(pdfText("F1", 10, 52, y, 0.08, 0.10, 0.14, "Refunded "+pdfMoney(refunded, inv.Currency)+" - Remaining "+pdfMoney(total-refunded, inv.Currency)))
		y -= 16
	}

	// Notes + footer.
	body.WriteString("0.96 0.97 0.98 rg\n40 44 532 88 re f\n")
	body.WriteString(pdfText("F2", 8, 52, 116, 0.45, 0.50, 0.55, "NOTES"))
	notes := append([]string{}, brand.Notes...)
	if status == "UNPAID" {
		notes = append(notes, "Payment status: Unpaid.")
	}
	if brand.TaxPercent > 0 {
		notes = append(notes, fmt.Sprintf("%s applied at %.2g%% on subtotal.", brand.TaxName, brand.TaxPercent))
	}
	if len(notes) > 5 {
		notes = notes[:5]
	}
	ny := 100.0
	for _, note := range notes {
		for _, wrap := range pdfWrap(note, 88) {
			if ny < 52 {
				break
			}
			body.WriteString(pdfText("F1", 9, 52, ny, 0.25, 0.30, 0.34, wrap))
			ny -= 12
		}
	}
	body.WriteString(pdfText("F1", 8, 40, 26, 0.45, 0.50, 0.55, brand.Footer))
	return pdfFile(body.String(), logo)
}

func pdfText(font string, size, x, y, r, g, b float64, text string) string {
	return fmt.Sprintf("BT\n/%s %.1f Tf\n%.3f %.3f %.3f rg\n1 0 0 1 %.1f %.1f Tm\n(%s) Tj\nET\n", font, size, r, g, b, x, y, pdfEscape(text))
}

func pdfRight(font string, size, right, y, r, g, b float64, text string) string {
	return pdfText(font, size, right-float64(len(text))*size*0.5, y, r, g, b, text)
}

func pdfMoney(cents int64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s %s%d.%02d", currency, sign, cents/100, cents%100)
}

func pdfWrap(v string, width int) []string {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return []string{"—"}
	}
	out := []string{}
	for len(v) > width {
		cut := strings.LastIndex(v[:width], " ")
		if cut < 8 {
			cut = width
		}
		out = append(out, strings.TrimSpace(v[:cut]))
		v = strings.TrimSpace(v[cut:])
	}
	if v != "" {
		out = append(out, v)
	}
	if len(out) > 6 {
		out = append(out[:5], out[5]+"...")
	}
	return out
}

func pdfFile(content string, logo *invoiceLogo) []byte {
	stream := []byte(content)
	objects := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R /F2 6 0 R >>" + pdfImageResource(logo) + " >> >>"),
		[]byte("<< /Length " + strconv.Itoa(len(stream)) + " >>\nstream\n" + content + "endstream"),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>"),
	}
	if logo != nil {
		objects = append(objects, pdfImageObject(logo))
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects)+1)
	for i, obj := range objects {
		offsets[i+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		out.Write(obj)
		out.WriteString("\nendobj\n")
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, start)
	return out.Bytes()
}

func pdfImageResource(logo *invoiceLogo) string {
	if logo == nil {
		return ""
	}
	return " /XObject << /Im1 7 0 R >>"
}

func pdfImageObject(logo *invoiceLogo) []byte {
	var compressed bytes.Buffer
	z, _ := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	_, _ = z.Write(logo.RGB)
	_ = z.Close()
	data := compressed.Bytes()
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", logo.Width, logo.Height, len(data))
	b.Write(data)
	b.WriteString("\nendstream")
	return b.Bytes()
}

func pdfEscape(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '(':
			b.WriteString(`\(`)
		case r == ')':
			b.WriteString(`\)`)
		case r == '\n', r == '\r', r == '\t', r == '\u00a0':
			b.WriteByte(' ')
		case r == '·' || r == '•' || r == '∙':
			b.WriteString(" - ")
		case r == '–' || r == '—' || r == '−':
			b.WriteByte('-')
		case r == '×':
			b.WriteByte('x')
		case r == '…':
			b.WriteString("...")
		case r >= 32 && r <= 126:
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
