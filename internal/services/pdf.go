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
		// Romanian diacritics — gofpdf's Latin-1 doesn't include comma-below
		// variants, so transliterate to plain ASCII.
		"ș", "s", "ț", "t", "ă", "a", "â", "a", "î", "i",
		"Ș", "S", "Ț", "T", "Ă", "A", "Â", "A", "Î", "I",
		// Other Unicode punctuation we use in the audit report — converted
		// to ASCII equivalents so the Latin-1 output renders cleanly.
		"—", "-", "–", "-", "✓", "OK", "·", "-", "…", "...",
		"„", "\"", "”", "\"", "’", "'",
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

// GenerateAuditReport produces a PDF copy of the ledger registry suitable for
// a regulator. Each event row shows a plain-Romanian description (no UUIDs)
// plus a short hash; the header carries the chain-verification result.
//
// nameByID maps user UUIDs to full names; apiaryNameByID maps apiary UUIDs to
// apiary names. Missing entries fall back to short hashes / generic labels.
func (s *PDFService) GenerateAuditReport(
	events []domain.LedgerEvent,
	nameByID map[string]string,
	apiaryNameByID map[string]string,
	verifyValid bool,
	verifyLastHash string,
	verifyCheckedAt time.Time,
	verifyError string,
) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	const usableW = 170.0 // A4 portrait 210 - 2x20

	// ── Title block ─────────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(usableW, 9, romanize("REGISTRU OFICIAL BEELIVE"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(usableW, 6, romanize("Raport de audit — lant de inregistrari SHA-256"), "", 1, "C", false, 0, "")
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "I", 9)
	pdf.CellFormat(usableW, 5, romanize(fmt.Sprintf("Generat: %s", time.Now().UTC().Format("02.01.2006 15:04 UTC"))), "", 1, "C", false, 0, "")
	pdf.Ln(6)

	// ── Verification banner ────────────────────────────────────────────────
	pdf.SetDrawColor(180, 180, 180)
	pdf.SetLineWidth(0.3)
	if verifyValid {
		pdf.SetFillColor(232, 248, 235) // pale green
		pdf.SetTextColor(22, 101, 52)
	} else {
		pdf.SetFillColor(254, 232, 232) // pale red
		pdf.SetTextColor(153, 27, 27)
	}
	pdf.Rect(20, pdf.GetY(), usableW, 18, "FD")

	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetXY(24, pdf.GetY()+3)
	if verifyValid {
		pdf.CellFormat(usableW-8, 6, romanize("✓  Toate inregistrarile sunt autentice"), "", 1, "L", false, 0, "")
	} else {
		pdf.CellFormat(usableW-8, 6, romanize("!  Atentie — modificare retroactiva detectata"), "", 1, "L", false, 0, "")
	}
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetX(24)
	checkedAt := verifyCheckedAt.Format("02.01.2006 15:04:05 UTC")
	if verifyValid {
		pdf.CellFormat(usableW-8, 5, romanize(fmt.Sprintf("%d actiuni verificate la %s", len(events), checkedAt)), "", 1, "L", false, 0, "")
		pdf.SetX(24)
		pdf.SetFont("Helvetica", "", 7)
		lastHash := verifyLastHash
		if len(lastHash) > 32 {
			lastHash = lastHash[:32] + "..."
		}
		pdf.CellFormat(usableW-8, 4, romanize(fmt.Sprintf("Ultim hash: %s", lastHash)), "", 1, "L", false, 0, "")
	} else {
		pdf.CellFormat(usableW-8, 5, romanize(fmt.Sprintf("Detaliu: %s", truncateRunes(verifyError, 90))), "", 1, "L", false, 0, "")
	}
	pdf.SetTextColor(0, 0, 0)
	pdf.SetY(pdf.GetY() + 6)
	pdf.Ln(4)

	// ── Events table ───────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(usableW, 6, romanize(fmt.Sprintf("Actiuni inregistrate (%d)", len(events))), "", 1, "L", false, 0, "")
	pdf.Ln(1)

	cols := []struct {
		label string
		w     float64
	}{
		{"Data si ora", 32},
		{"Actiune", 116},
		{"Cod oficial", 22},
	}

	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(245, 240, 248)
	pdf.SetDrawColor(160, 160, 160)
	for _, c := range cols {
		pdf.CellFormat(c.w, 7, romanize(c.label), "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	for i, e := range events {
		// Alternate row tint for readability.
		if i%2 == 1 {
			pdf.SetFillColor(252, 250, 254)
		} else {
			pdf.SetFillColor(255, 255, 255)
		}

		title, subtitle := describeEventRO(e, nameByID, apiaryNameByID)
		desc := title
		if subtitle != "" {
			desc = title + " — " + subtitle
		}
		desc = truncateRunes(desc, 110)
		shortHash := e.Hash
		if len(shortHash) > 12 {
			shortHash = shortHash[:12]
		}

		pdf.CellFormat(cols[0].w, 6, e.CreatedAt.Local().Format("02.01.2006 15:04"), "1", 0, "L", true, 0, "")
		pdf.CellFormat(cols[1].w, 6, romanize(desc), "1", 0, "L", true, 0, "")
		pdf.CellFormat(cols[2].w, 6, "#"+shortHash, "1", 1, "L", true, 0, "")
	}

	pdf.Ln(6)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(120, 120, 120)
	footer := romanize(
		"Acest raport este o copie a registrului oficial. Fiecare actiune este " +
			"semnata criptografic si legata de cea anterioara prin hash SHA-256, " +
			"astfel incat orice modificare retroactiva sa fie detectabila. " +
			"Verificarea integritatii poate fi reluata oricand din interfata web.",
	)
	pdf.MultiCell(usableW, 4, footer, "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf audit output: %w", err)
	}
	return buf.Bytes(), nil
}

// GenerateDamageInspectionReport produces an A4 PDF describing one damage
// claim plus the inspector's confirmation that they have visited the site.
// Used as an email attachment sent to the primărie when the inspector
// approves a claim.
func (s *PDFService) GenerateDamageInspectionReport(
	claim domain.DamageClaim,
	beekeeper domain.User,
	apiary domain.Apiary,
	relatedSpray *domain.SprayReport,
	relatedFarmer *domain.User,
	inspector domain.User,
	photoCount int,
	inspectedAt time.Time,
) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(22, 22, 22)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	const usableW = 166.0

	// Title block
	pdf.SetFont("Helvetica", "B", 15)
	pdf.CellFormat(usableW, 9, romanize("RAPORT INSPECTIE PAGUBA"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(usableW, 6, romanize("Sistem BeeLive - notificari pesticide"), "", 1, "C", false, 0, "")
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "", 9)
	docNumber := strings.ToUpper(strings.ReplaceAll(claim.ID, "-", ""))
	if len(docNumber) > 12 {
		docNumber = docNumber[:12]
	}
	pdf.CellFormat(usableW, 5, romanize(fmt.Sprintf("Nr. dosar: %s | Data inspectiei: %s", docNumber, inspectedAt.Format("02.01.2006 15:04"))), "", 1, "C", false, 0, "")
	pdf.Ln(6)

	// Section helper
	writeRow := func(label, value string) {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(45, 6, romanize(label), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(usableW-45, 6, romanize(value), "", "L", false)
	}

	sectionHeader := func(label string) {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.SetTextColor(133, 72, 157) // brand purple
		pdf.CellFormat(usableW, 6, romanize(label), "", 1, "L", false, 0, "")
		pdf.SetDrawColor(133, 72, 157)
		pdf.SetLineWidth(0.4)
		pdf.Line(pdf.GetX(), pdf.GetY(), pdf.GetX()+usableW, pdf.GetY())
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(2)
	}

	// Beekeeper section
	sectionHeader("Apicultor reclamant")
	writeRow("Nume:", beekeeper.FullName)
	writeRow("Localitate:", fmt.Sprintf("%s, jud. %s", beekeeper.Locality, beekeeper.County))
	if beekeeper.Phone != "" {
		writeRow("Telefon:", beekeeper.Phone)
	}
	if beekeeper.Email != "" {
		writeRow("Email:", beekeeper.Email)
	}

	// Apiary section
	sectionHeader("Stupina afectata")
	writeRow("Denumire:", apiary.Name)
	writeRow("Coordonate:", fmt.Sprintf("%.5f, %.5f", apiary.Lat, apiary.Lng))
	writeRow("Numar stupi:", fmt.Sprintf("%d", apiary.HiveCount))

	// Claim section
	sectionHeader("Detalii paguba raportata")
	writeRow("Data depunerii:", claim.CreatedAt.Format("02.01.2006 15:04"))
	writeRow("Stupi afectati:", fmt.Sprintf("%d", claim.HiveLossCount))
	writeRow("Coordonate GPS:", fmt.Sprintf("%.5f, %.5f", claim.GpsLat, claim.GpsLng))
	writeRow("Fotografii atasate:", fmt.Sprintf("%d", photoCount))
	pdf.Ln(1)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(usableW, 6, romanize("Descriere reclamant:"), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.MultiCell(usableW, 5, romanize(claim.Description), "", "L", false)

	// Related spray section, if any
	if relatedSpray != nil {
		sectionHeader("Stropire suspectata")
		writeRow("Substanta:", relatedSpray.Substance)
		writeRow("Toxicitate:", string(relatedSpray.Toxicity))
		writeRow("Suprafata:", fmt.Sprintf("%.2f ha", relatedSpray.SurfaceHA))
		writeRow("Data programata:", relatedSpray.ScheduledAt.Format("02.01.2006 15:04"))
		if relatedFarmer != nil {
			writeRow("Fermier:", relatedFarmer.FullName)
			writeRow("Localitate fermier:", fmt.Sprintf("%s, jud. %s", relatedFarmer.Locality, relatedFarmer.County))
		}
	}

	// Inspector confirmation
	sectionHeader("Confirmare inspectie")
	pdf.SetFont("Helvetica", "", 10)
	pdf.MultiCell(usableW, 5, romanize(fmt.Sprintf(
		"Subsemnatul %s, inspector ANSVSA judetul %s, confirm ca am preluat aceasta "+
			"reclamatie spre analiza la data de %s. Dosarul intra in procedura "+
			"administrativa standard si va fi instrumentat conform legislatiei in vigoare.",
		inspector.FullName,
		inspector.County,
		inspectedAt.Format("02.01.2006"),
	)), "", "L", false)

	pdf.Ln(8)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(usableW/2, 5, romanize("Inspector:"), "", 0, "L", false, 0, "")
	pdf.CellFormat(usableW/2, 5, romanize("Semnatura electronica:"), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(usableW/2, 5, romanize(inspector.FullName), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8)
	pdf.CellFormat(usableW/2, 5, romanize(fmt.Sprintf("Cod oficial: #%s", truncateRunes(claim.LedgerHash, 12))), "", 1, "L", false, 0, "")

	// Footer
	pdf.Ln(8)
	pdf.SetFont("Helvetica", "I", 7)
	pdf.SetTextColor(120, 120, 120)
	pdf.MultiCell(usableW, 4, romanize(
		"Document generat automat de sistemul BeeLive. Inregistrarea pagubei "+
			"este parte din registrul oficial cu lant criptografic SHA-256; orice "+
			"modificare retroactiva poate fi detectata prin verificarea publica "+
			"a lantului.",
	), "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf damage inspection output: %w", err)
	}
	return buf.Bytes(), nil
}

// describeEventRO produces a plain-Romanian description for a ledger event,
// using human names where available. Matches the frontend describe() logic.
func describeEventRO(
	e domain.LedgerEvent,
	nameByID map[string]string,
	apiaryNameByID map[string]string,
) (string, string) {
	actor := "Sistemul"
	if e.ActorID != nil {
		if n, ok := nameByID[*e.ActorID]; ok {
			actor = n
		} else {
			actor = "Utilizator"
		}
	}

	getString := func(key string) string {
		if v, ok := e.Payload[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	getNumber := func(key string) (float64, bool) {
		v, ok := e.Payload[key]
		if !ok {
			return 0, false
		}
		f, ok := v.(float64)
		return f, ok
	}

	apiaryName := func() string {
		id := getString("apiary_id")
		if id == "" {
			return ""
		}
		if n, ok := apiaryNameByID[id]; ok {
			return n
		}
		return ""
	}

	switch e.Type {
	case "spray.created":
		subst := getString("substance")
		tox := getString("toxicity")
		sub := subst
		if tox != "" {
			sub = fmt.Sprintf("%s (%s)", subst, tox)
		}
		return actor + " a anuntat o stropire", sub
	case "spray.cancelled":
		return actor + " a anulat o stropire programata", ""
	case "alert.dispatched":
		ap := apiaryName()
		sub := ""
		if ap != "" {
			sub = "Stupina vizata: " + ap
		}
		return "Alerta trimisa apicultorilor afectati", sub
	case "alert.confirmed":
		method := getString("method")
		methodRO := method
		switch method {
		case "call":
			methodRO = "telefon"
		case "sms":
			methodRO = "SMS"
		case "app":
			methodRO = "aplicatie"
		}
		sub := ""
		if methodRO != "" {
			sub = "Prin: " + methodRO
		}
		return actor + " a confirmat primirea alertei", sub
	case "damage.filed":
		ap := apiaryName()
		var parts []string
		if ap != "" {
			parts = append(parts, ap)
		}
		if loss, ok := getNumber("hive_loss"); ok {
			parts = append(parts, fmt.Sprintf("%.0f stupi afectati", loss))
		}
		return actor + " a depus o cerere de paguba", strings.Join(parts, " · ")
	case "pdf.generated":
		return "Raport PDF oficial generat pentru primarie", ""
	case "email.sent":
		return "Email oficial trimis catre primarie", ""
	case "apiary.registered":
		name := getString("name")
		return actor + " a inregistrat o stupina", name
	default:
		return e.Type, ""
	}
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
