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
	pdf.SetMargins(20, 20, 20)
	pdf.AddPage()

	// Title
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(170, 10, romanize("NOTIFICARE TRATAMENT PESTICID"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 8)
	pdf.CellFormat(170, 6, romanize("Sistem BeeLive (beelive.ro) — document generat automat"), "", 1, "C", false, 0, "")
	pdf.Ln(4)

	// Table helper
	pdf.SetFont("Helvetica", "", 10)
	row := func(label, value string) {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(60, 8, romanize(label), "1", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.CellFormat(110, 8, romanize(value), "1", 1, "L", false, 0, "")
	}

	row("Data tratamentului", spray.ScheduledAt.Format("02.01.2006 15:04"))
	row("Substanta", spray.Substance)
	row("Toxicitate", string(spray.Toxicity))
	row("Suprafata (ha)", fmt.Sprintf("%.2f", spray.SurfaceHA))
	row("Durata (ore)", fmt.Sprintf("%.1f", spray.DurationHours))
	row("Fermier", farmer.FullName)
	row("CNP fermier", maskCNP(farmer.CNP))
	row("Parcela", parcel.Name)
	row("Judet", parcel.County)
	row("Localitate", parcel.Locality)
	row("Apiarii afectate", fmt.Sprintf("%d", affectedCount))
	row("Hash registru", ledgerHash)

	pdf.Ln(8)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(170, 6, romanize(fmt.Sprintf("Generat la: %s", time.Now().UTC().Format("02.01.2006 15:04:05 UTC"))), "", 1, "C", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf output: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *PDFService) GenerateANFExport(sprays []domain.SprayReport, farmer domain.User, ledgerHash string) ([]byte, error) {
	pdf := gofpdf.New("L", "mm", "A4", "") // landscape for table
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 13)
	pdf.CellFormat(267, 10, romanize("EXPORT ANF - RAPOARTE TRATAMENTE"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.CellFormat(267, 6, romanize(fmt.Sprintf("Fermier: %s | Hash: %s", farmer.FullName, ledgerHash)), "", 1, "C", false, 0, "")
	pdf.Ln(4)

	// Header row
	pdf.SetFont("Helvetica", "B", 9)
	cols := []struct {
		label string
		w     float64
	}{
		{"ID", 50}, {"Data", 28}, {"Substanta", 40}, {"Toxicitate", 22},
		{"Suprafata", 22}, {"Status", 25}, {"Hash", 80},
	}
	for _, c := range cols {
		pdf.CellFormat(c.w, 8, c.label, "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	for _, sp := range sprays {
		pdf.CellFormat(50, 7, sp.ID, "1", 0, "L", false, 0, "")
		pdf.CellFormat(28, 7, sp.ScheduledAt.Format("02.01.2006"), "1", 0, "C", false, 0, "")
		pdf.CellFormat(40, 7, romanize(sp.Substance), "1", 0, "L", false, 0, "")
		pdf.CellFormat(22, 7, string(sp.Toxicity), "1", 0, "C", false, 0, "")
		pdf.CellFormat(22, 7, fmt.Sprintf("%.2f", sp.SurfaceHA), "1", 0, "R", false, 0, "")
		pdf.CellFormat(25, 7, romanize(string(sp.Status)), "1", 0, "C", false, 0, "")
		pdf.CellFormat(80, 7, sp.LedgerHash, "1", 1, "L", false, 0, "")
	}

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(267, 6, romanize(fmt.Sprintf("Total: %d rapoarte | Generat: %s", len(sprays), time.Now().UTC().Format("02.01.2006 15:04:05 UTC"))), "", 1, "C", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf anf output: %w", err)
	}
	return buf.Bytes(), nil
}
