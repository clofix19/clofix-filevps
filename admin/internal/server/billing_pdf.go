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
	taxName := d.Currency.TaxName
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
	if taxName == "" {
		taxName = "Tax"
	}
	inv = enrichInvoice(inv, rate)
	raw := invoicePDF(inv, taxName, refunded, loadInvoiceLogo(s.UIDir))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(raw)
}

type invoiceLogo struct {
	Width  int
	Height int
	RGB    []byte
}

func loadInvoiceLogo(uiDir string) *invoiceLogo {
	if uiDir == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(uiDir, "clofix-logo.png"))
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
	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			alpha := float64(a) / 65535
			i := (y*w + x) * 3
			rgb[i] = byte(float64(r>>8)*alpha + 10*(1-alpha))
			rgb[i+1] = byte(float64(g>>8)*alpha + 15*(1-alpha))
			rgb[i+2] = byte(float64(b>>8)*alpha + 22*(1-alpha))
		}
	}
	return &invoiceLogo{Width: w, Height: h, RGB: rgb}
}

func invoicePDF(inv billingInvoice, taxName string, refunded int64, logo *invoiceLogo) []byte {
	stampInvoiceMoney(&inv, inv.FXBDTPerUSD)
	total := inv.AmountCents + inv.TaxCents
	issued := inv.Created.UTC().Format("02 Jan 2006")
	status := strings.ToUpper(inv.Status)
	if status == "" {
		status = "DRAFT"
	}
	desc := strings.TrimSpace(inv.Description)
	if desc == "" {
		desc = "CloFix Cloud service"
	}
	var body strings.Builder
	body.WriteString("0.039 0.059 0.086 rg\n0 704 612 88 re f\n")
	body.WriteString("0.212 0.722 0.647 rg\n0 704 612 4 re f\n")
	if logo != nil {
		body.WriteString("q\n150 0 0 56 36 724 cm\n/Im1 Do\nQ\n")
	} else {
		body.WriteString(pdfText("F2", 20, 36, 746, 1, 1, 1, "CloFix Cloud"))
	}
	body.WriteString(pdfRight("F2", 26, 564, 748, 1, 1, 1, "INVOICE"))
	body.WriteString(pdfRight("F1", 10, 564, 728, 0.82, 0.90, 0.88, status))
	body.WriteString(pdfText("F2", 9, 48, 668, 0.40, 0.45, 0.50, "INVOICE NUMBER"))
	body.WriteString(pdfText("F1", 11, 48, 652, 0.08, 0.10, 0.14, inv.ID))
	body.WriteString(pdfText("F2", 9, 250, 668, 0.40, 0.45, 0.50, "INVOICE DATE"))
	body.WriteString(pdfText("F1", 11, 250, 652, 0.08, 0.10, 0.14, issued))
	body.WriteString(pdfText("F2", 9, 420, 668, 0.40, 0.45, 0.50, "STATUS"))
	body.WriteString(pdfText("F1", 11, 420, 652, 0.08, 0.10, 0.14, status))
	body.WriteString("0.93 0.95 0.96 rg\n48 560 250 70 re f\n")
	body.WriteString(pdfText("F2", 9, 60, 612, 0.40, 0.45, 0.50, "BILL TO"))
	body.WriteString(pdfText("F2", 12, 60, 594, 0.08, 0.10, 0.14, inv.Owner))
	if inv.SubscriptionID != "" {
		body.WriteString(pdfText("F1", 9, 60, 578, 0.35, 0.40, 0.45, "Subscription "+inv.SubscriptionID))
	}
	if !inv.PeriodStart.IsZero() {
		body.WriteString(pdfText("F1", 9, 420, 636, 0.35, 0.40, 0.45, inv.PeriodStart.UTC().Format("02 Jan")+" - "+inv.PeriodEnd.UTC().Format("02 Jan 2006")))
	}
	body.WriteString(pdfText("F2", 9, 48, 532, 0.40, 0.45, 0.50, "DESCRIPTION"))
	y := 514.0
	for _, line := range pdfWrap(desc, 86) {
		body.WriteString(pdfText("F1", 11, 48, y, 0.08, 0.10, 0.14, line))
		y -= 16
	}
	y -= 12
	body.WriteString(fmt.Sprintf("0.039 0.059 0.086 rg\n48 %.1f 516 22 re f\n", y-6))
	body.WriteString(pdfText("F2", 9, 60, y, 1, 1, 1, "Description"))
	body.WriteString(pdfText("F2", 9, 300, y, 1, 1, 1, "Resource"))
	body.WriteString(pdfRight("F2", 9, 470, y, 1, 1, 1, "USD"))
	body.WriteString(pdfRight("F2", 9, 548, y, 1, 1, 1, "BDT"))
	y -= 28
	lines := inv.Lines
	if len(lines) == 0 {
		item := desc
		if inv.SubscriptionID != "" {
			item = desc + " / " + inv.SubscriptionID
		}
		lines = []billingInvoiceLine{{Description: item, ResourceID: inv.ResourceID, AmountCents: inv.AmountCents, USDCents: inv.USDCents, BDTCents: inv.BDTCents}}
	}
	if len(lines) > 8 {
		lines = lines[:8]
	}
	for _, line := range lines {
		label := line.Description
		if line.Quantity > 0 && line.Unit != "" {
			label = fmt.Sprintf("%.2g %s %s", line.Quantity, line.Unit, line.Description)
		}
		wrapped := pdfWrap(label, 36)
		body.WriteString(pdfText("F1", 9, 60, y, 0.08, 0.10, 0.14, wrapped[0]))
		resource := line.ResourceID
		if resource == "" {
			resource = line.UsageType
		}
		if resource == "" {
			resource = "—"
		}
		body.WriteString(pdfText("F1", 8, 300, y, 0.08, 0.10, 0.14, resource))
		body.WriteString(pdfRight("F1", 9, 470, y, 0.08, 0.10, 0.14, pdfMoney(line.USDCents, "USD")))
		body.WriteString(pdfRight("F1", 9, 548, y, 0.08, 0.10, 0.14, pdfMoney(line.BDTCents, "BDT")))
		y -= 14
		for _, extra := range wrapped[1:] {
			body.WriteString(pdfText("F1", 8, 60, y, 0.35, 0.40, 0.45, extra))
			y -= 12
		}
	}
	y -= 8
	body.WriteString(fmt.Sprintf("0.86 0.89 0.91 RG\n0.6 w\n48 %.1f 516 0 re S\n", y+8))
	y -= 8
	body.WriteString(pdfText("F1", 10, 300, y, 0.35, 0.40, 0.45, "Subtotal"))
	body.WriteString(pdfRight("F1", 10, 548, y, 0.08, 0.10, 0.14, pdfMoney(inv.AmountCents, inv.Currency)))
	y -= 18
	body.WriteString(pdfText("F1", 10, 300, y, 0.35, 0.40, 0.45, taxName))
	body.WriteString(pdfRight("F1", 10, 548, y, 0.08, 0.10, 0.14, pdfMoney(inv.TaxCents, inv.Currency)))
	y -= 24
	body.WriteString(fmt.Sprintf("0.212 0.722 0.647 rg\n300 %.1f 264 40 re f\n", y-20))
	body.WriteString(pdfText("F2", 11, 312, y, 1, 1, 1, "Total"))
	body.WriteString(pdfRight("F2", 10, 548, y, 1, 1, 1, pdfMoney(total, inv.Currency)))
	y -= 16
	body.WriteString(pdfRight("F1", 9, 548, y, 1, 1, 1, pdfMoney(inv.USDCents, "USD")+" / "+pdfMoney(inv.BDTCents, "BDT")))
	y -= 36
	if inv.Paid != nil {
		body.WriteString(pdfText("F1", 10, 48, y, 0.08, 0.10, 0.14, "Paid on "+inv.Paid.UTC().Format("02 Jan 2006 15:04 UTC")+" - manual bookkeeping"))
		y -= 16
	}
	if refunded > 0 {
		body.WriteString(pdfText("F1", 10, 48, y, 0.08, 0.10, 0.14, "Refunded "+pdfMoney(refunded, inv.Currency)+" - remaining "+pdfMoney(total-refunded, inv.Currency)))
		y -= 16
	}
	body.WriteString("0.93 0.95 0.96 rg\n48 48 516 88 re f\n")
	body.WriteString(pdfText("F2", 9, 60, 118, 0.40, 0.45, 0.50, "NOTES"))
	notes := []string{"This invoice lists stored CloFix controller fields only.", "Card charges are not processed in this version."}
	if inv.Status == "draft" {
		notes = append(notes, "Draft invoices are unpaid bookkeeping records.")
	}
	ny := 102.0
	for _, note := range notes {
		body.WriteString(pdfText("F1", 9, 60, ny, 0.25, 0.30, 0.34, note))
		ny -= 13
	}
	body.WriteString(pdfText("F1", 8, 48, 28, 0.45, 0.50, 0.55, "CloFix Cloud  -  Generated from Billing & Payments"))
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
		case r == '\n', r == '\r', r == '\t':
			b.WriteByte(' ')
		case r >= 32 && r <= 126:
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}
