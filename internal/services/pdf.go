package services

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
	"github.com/radarul-albinelor/api/internal/domain"
)

type PDFService struct {
	appBaseURL string
}

func NewPDFService(appBaseURL string) *PDFService {
	return &PDFService{appBaseURL: appBaseURL}
}

func romanize(s string) string {
	return strings.NewReplacer(
		"ș", "s", "ț", "t", "ă", "a", "â", "a", "î", "i",
		"Ș", "S", "Ț", "T", "Ă", "A", "Â", "A", "Î", "I",
	).Replace(s)
}

func maskCNP(cnp string) string {
	if len(cnp) < 2 {
		return "***"
	}
	return strings.Repeat("*", len(cnp)-2) + cnp[len(cnp)-2:]
}

func (s *PDFService) GeneratePrimariePDF(spray domain.SprayReport, farmer domain.User, parcel domain.Parcel, affectedCount int, ledgerHash string) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(25, 25, 25)
	pdf.AddPage()

	docNumber := strings.ToUpper(strings.ReplaceAll(spray.ID, "-", ""))
	if len(docNumber) > 8 {
		docNumber = docNumber[:8]
	}

	// ── Header (top-left): who is filing + document number ──
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 6, romanize(strings.ToUpper(farmer.FullName)), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(0, 6, romanize(fmt.Sprintf("Nr. %s din %s", docNumber, spray.CreatedAt.Format("02.01.2006"))), "", 1, "L", false, 0, "")

	pdf.Ln(16)

	// ── Title ──
	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(0, 10, romanize("ÎNȘTIINȚARE"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "I", 10)
	pdf.CellFormat(0, 6, romanize("Cu privire la tratamente fitosanitare"), "", 1, "C", false, 0, "")
	pdf.Ln(8)

	// ── Addressee ──
	pdf.SetFont("Helvetica", "", 12)
	pdf.CellFormat(0, 8, romanize(fmt.Sprintf("Către Primăria %s", parcel.Locality)), "", 1, "C", false, 0, "")
	pdf.Ln(10)

	// ── Body ──
	const bodySize = 11.0
	pdf.SetFont("Helvetica", "", bodySize)

	intro := fmt.Sprintf(
		"    Subscrisul %s, domiciliat în %s, județul %s, CNP %s, în calitate de exploatator agricol, vă înștiințez că în data de %s, pe o durată estimată de %.1f ore, voi efectua tratament fitosanitar prin stropire la cultura de %s pe parcela \"%s\" (nr. cadastral %s, %.2f ha), cu următorul produs:",
		farmer.FullName,
		parcel.Locality,
		parcel.County,
		maskCNP(farmer.CNP),
		spray.ScheduledAt.Format("02.01.2006 ora 15:04"),
		spray.DurationHours,
		spray.Crop,
		parcel.Name,
		parcel.CadastralNumber,
		spray.SurfaceHA,
	)
	pdf.MultiCell(0, 6, romanize(intro), "", "J", false)
	pdf.Ln(3)

	// Indented product line
	pdf.SetFont("Helvetica", "B", bodySize)
	pdf.SetX(45)
	pdf.MultiCell(0, 6, romanize(fmt.Sprintf("%s — toxicitate %s", spray.Substance, string(spray.Toxicity))), "", "L", false)
	pdf.Ln(4)

	// Closing paragraph
	pdf.SetFont("Helvetica", "", bodySize)
	closing := fmt.Sprintf(
		"    Toți crescătorii de albine din zonă sunt rugați să își ia măsurile necesare de protecție a albinelor. Prin sistemul BeeLive (beelive.ro), %d apicultori aflați în raza de risc au fost notificați automat în momentul înregistrării prezentei.",
		affectedCount,
	)
	pdf.MultiCell(0, 6, romanize(closing), "", "J", false)

	pdf.Ln(18)

	// ── Footer: date + signature line ──
	pdf.SetFont("Helvetica", "", bodySize)
	pdf.CellFormat(80, 6, romanize(fmt.Sprintf("Data: %s", time.Now().Format("02.01.2006"))), "", 0, "L", false, 0, "")
	pdf.CellFormat(0, 6, romanize("Semnătură:"), "", 1, "R", false, 0, "")

	// Signature underline (right side)
	pdf.SetX(140)
	pdf.CellFormat(45, 14, "", "B", 1, "R", false, 0, "")

	// ── BeeLive trailer with ledger hash for tamper-evidence ──
	pdf.Ln(18)
	pdf.SetDrawColor(180, 180, 180)
	pdf.Line(25, pdf.GetY(), 185, pdf.GetY())
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "I", 7)
	pdf.CellFormat(0, 4, romanize("Document generat automat de sistemul BeeLive (beelive.ro). Înregistrare în registru distribuit, hash SHA-256:"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 6)
	pdf.CellFormat(0, 4, ledgerHash, "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "I", 7)
	pdf.CellFormat(0, 4, romanize(fmt.Sprintf("Generat la: %s", time.Now().UTC().Format("02.01.2006 15:04:05 UTC"))), "", 1, "C", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf output: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *PDFService) GenerateANFExport(sprays []domain.SprayReport, farmer domain.User, ledgerHash string) ([]byte, error) {
	pdf := gofpdf.New("L", "mm", "A4", "") // landscape for table
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddPage()

	const usableW = 267.0 // A4 landscape 297 minus 2×15 margin

	pdf.SetFont("Helvetica", "B", 13)
	pdf.CellFormat(usableW, 10, romanize("EXPORT ANF - RAPOARTE TRATAMENTE"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	headerHash := ledgerHash
	if headerHash == "" {
		headerHash = "—"
	}
	pdf.CellFormat(usableW, 6, romanize(fmt.Sprintf("Fermier: %s | Hash: %s", farmer.FullName, headerHash)), "", 1, "C", false, 0, "")
	pdf.Ln(4)

	// Column widths sized so the longest realistic value fits at the body font
	// (Helvetica 7pt ≈ 1.5mm/char). UUIDs are 36 chars (~54mm), SHA-256 hex
	// hashes are 64 chars (~96mm) — those are the two that used to overflow.
	cols := []struct {
		label string
		w     float64
	}{
		{"ID", 58},
		{"Data", 22},
		{"Substanta", 38},
		{"Toxicitate", 16},
		{"Suprafata", 16},
		{"Status", 20},
		{"Hash", 97}, // 58+22+38+16+16+20+97 = 267
	}

	// Header row
	pdf.SetFont("Helvetica", "B", 8)
	for _, c := range cols {
		pdf.CellFormat(c.w, 8, c.label, "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)

	// Body
	pdf.SetFont("Helvetica", "", 7)
	for _, sp := range sprays {
		pdf.CellFormat(cols[0].w, 7, sp.ID, "1", 0, "L", false, 0, "")
		pdf.CellFormat(cols[1].w, 7, sp.ScheduledAt.Format("02.01.2006"), "1", 0, "C", false, 0, "")
		pdf.CellFormat(cols[2].w, 7, romanize(truncateRunes(sp.Substance, 30)), "1", 0, "L", false, 0, "")
		pdf.CellFormat(cols[3].w, 7, string(sp.Toxicity), "1", 0, "C", false, 0, "")
		pdf.CellFormat(cols[4].w, 7, fmt.Sprintf("%.2f", sp.SurfaceHA), "1", 0, "R", false, 0, "")
		pdf.CellFormat(cols[5].w, 7, romanize(string(sp.Status)), "1", 0, "C", false, 0, "")
		pdf.CellFormat(cols[6].w, 7, sp.LedgerHash, "1", 1, "L", false, 0, "")
	}

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(usableW, 6, romanize(fmt.Sprintf("Total: %d rapoarte | Generat: %s", len(sprays), time.Now().UTC().Format("02.01.2006 15:04:05 UTC"))), "", 1, "C", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf anf output: %w", err)
	}
	return buf.Bytes(), nil
}

// truncateRunes caps a string to n runes, appending an ellipsis when cut. Rune-
// safe so Romanian diacritics survive without splitting a UTF-8 sequence.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
