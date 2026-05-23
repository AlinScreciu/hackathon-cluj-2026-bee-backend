# Phase 9 — External Integrations

**Status**: PENDING
**Goal**: Real Twilio calls/SMS, ElevenLabs Romanian TTS, Web Push notifications, email with PDF attachment, Twilio webhook signature validation.

---

## Current State (before this phase)

Phases 1-8 complete. Cascade works with mocks (logs to stdout). Now wire the real external services.

What exists:
- `internal/external/twilio/` — empty/stub directory
- `internal/external/elevenlabs/` — empty/stub directory
- `internal/external/webpush/` — empty/stub directory
- `internal/services/cascade.go` — `CascadeService` with `notifier Notifier` and `pusher PushSender` both nil
- `internal/services/` — no PDF service yet
- `internal/api/router.go` — `NewCascadeService(pool, ledger, nil, nil, appBaseURL)`
- `internal/api/sprays.go` — no PDF generation, no email
- `internal/api/apiaries.go` — `POST /apiaries` stub (501)
- `cmd/server/main.go` — does NOT create `uploads/` directories

Dependencies already in `go.mod`:
- `github.com/SherClockHolmes/webpush-go v1.4.0`
- `github.com/jung-kurt/gofpdf v1.16.2`
- `github.com/wneessen/go-mail v0.7.3` (already used in Phase 4)
- `github.com/twilio/twilio-go v1.30.9` (in go.mod but NOT used — we implement manually via net/http)

Config values used in this phase:
- `cfg.TwilioAccountSID`, `cfg.TwilioAuthToken`, `cfg.TwilioFromPhone`
- `cfg.ElevenLabsAPIKey`, `cfg.ElevenLabsVoiceID`
- `cfg.VAPIDPublicKey`, `cfg.VAPIDPrivateKey`
- `cfg.ResendAPIKey`, `cfg.ResendFromEmail`
- `cfg.PrimarieEmail`
- `cfg.AppBaseURL`

The `Notifier` and `PushSender` interfaces are defined in `internal/services/cascade.go`:
```go
type Notifier interface {
    SendSMS(ctx context.Context, to, body string) (string, error)
    MakeCall(ctx context.Context, to, twimlURL string) (string, error)
}
type PushSender interface {
    Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error
}
```

---

## Prerequisites

Phases 1-8 complete. External service credentials in `.env`:
```
TWILIO_ACCOUNT_SID=ACxxxxxxxx
TWILIO_AUTH_TOKEN=xxxxxxxx
TWILIO_FROM_PHONE=+40xxxxxxx
ELEVENLABS_API_KEY=sk_xxxxxxxx
ELEVENLABS_VOICE_ID=21m00Tcm4TlvDq8ikWAM
VAPID_PUBLIC_KEY=<generated via make gen-vapid>
VAPID_PRIVATE_KEY=<generated via make gen-vapid>
RESEND_API_KEY=re_xxxxxxxx
PRIMARIE_EMAIL=primarie@test.com
APP_BASE_URL=https://your-tunnel.trycloudflare.com
```

---

## Files to Create

### `internal/external/twilio/client.go`

Package: `twilio`

```go
package twilio

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "sort"
    "strings"
    "time"
    
    "crypto/hmac"
    "crypto/sha1"
    "encoding/base64"
)

// Client implements cascade.Notifier.
type Client struct {
    AccountSID string
    AuthToken  string
    FromPhone  string
    httpClient *http.Client
}

func New(accountSID, authToken, fromPhone string) *Client {
    return &Client{
        AccountSID: accountSID,
        AuthToken:  authToken,
        FromPhone:  fromPhone,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

// SendSMS sends an SMS via Twilio and returns the Message SID.
func (c *Client) SendSMS(ctx context.Context, to, body string) (string, error) {
    endpoint := fmt.Sprintf(
        "https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json",
        c.AccountSID)

    form := url.Values{}
    form.Set("From", c.FromPhone)
    form.Set("To", to)
    form.Set("Body", body)

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
        strings.NewReader(form.Encode()))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.SetBasicAuth(c.AccountSID, c.AuthToken)

    resp, err := c.httpClient.Do(req)
    if err != nil { return "", fmt.Errorf("twilio SMS: %w", err) }
    defer resp.Body.Close()

    raw, _ := io.ReadAll(resp.Body)
    if resp.StatusCode >= 400 {
        return "", fmt.Errorf("twilio SMS error %d: %s", resp.StatusCode, raw)
    }

    var result struct { SID string `json:"sid"` }
    if err := json.Unmarshal(raw, &result); err != nil { return "", err }
    return result.SID, nil
}

// MakeCall initiates a Twilio voice call and returns the Call SID.
// twimlURL is the URL Twilio will fetch for TwiML instructions.
func (c *Client) MakeCall(ctx context.Context, to, twimlURL string) (string, error) {
    endpoint := fmt.Sprintf(
        "https://api.twilio.com/2010-04-01/Accounts/%s/Calls.json",
        c.AccountSID)

    form := url.Values{}
    form.Set("From", c.FromPhone)
    form.Set("To", to)
    form.Set("Url", twimlURL)
    // Status callback so we receive call completion events
    statusCB := strings.Replace(twimlURL, "/voice/gather", "/voice/status", 1)
    form.Set("StatusCallback", statusCB)
    form.Set("StatusCallbackMethod", "POST")

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
        strings.NewReader(form.Encode()))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.SetBasicAuth(c.AccountSID, c.AuthToken)

    resp, err := c.httpClient.Do(req)
    if err != nil { return "", fmt.Errorf("twilio call: %w", err) }
    defer resp.Body.Close()

    raw, _ := io.ReadAll(resp.Body)
    if resp.StatusCode >= 400 {
        return "", fmt.Errorf("twilio call error %d: %s", resp.StatusCode, raw)
    }

    var result struct { SID string `json:"sid"` }
    if err := json.Unmarshal(raw, &result); err != nil { return "", err }
    return result.SID, nil
}

// ValidateSignature validates a Twilio webhook request signature.
// Reference: https://www.twilio.com/docs/usage/webhooks/webhooks-security
//
// Algorithm:
// 1. Build the URL + sorted POST params string:
//    url + sorted_key_value_pairs (no separator between key+value, sorted by key)
// 2. HMAC-SHA1 of that string using authToken as key
// 3. Base64 encode
// 4. Compare to X-Twilio-Signature header
func (c *Client) ValidateSignature(fullURL string, formParams map[string]string, signature string) bool {
    // Build string to sign
    keys := make([]string, 0, len(formParams))
    for k := range formParams { keys = append(keys, k) }
    sort.Strings(keys)
    
    s := fullURL
    for _, k := range keys {
        s += k + formParams[k]
    }
    
    mac := hmac.New(sha1.New, []byte(c.AuthToken))
    mac.Write([]byte(s))
    expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
    
    return hmac.Equal([]byte(expected), []byte(signature))
}
```

### `internal/external/elevenlabs/client.go`

Package: `elevenlabs`

```go
package elevenlabs

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "os"
    "path/filepath"
    "time"
)

// Client calls ElevenLabs Text-to-Speech API and caches results to disk.
type Client struct {
    APIKey     string
    VoiceID    string
    httpClient *http.Client
    cacheDir   string // e.g. "uploads/voice"
}

func New(apiKey, voiceID, cacheDir string) *Client {
    return &Client{
        APIKey:     apiKey,
        VoiceID:    voiceID,
        httpClient: &http.Client{Timeout: 30 * time.Second},
        cacheDir:   cacheDir,
    }
}

// TextToSpeech converts text to MP3 audio bytes.
// Returns cached bytes if the text has been synthesized before.
func (c *Client) TextToSpeech(ctx context.Context, text string) ([]byte, error) {
    cacheKey := c.sha256hex(text)
    cachePath := filepath.Join(c.cacheDir, cacheKey+".mp3")

    // Check cache
    if data, err := os.ReadFile(cachePath); err == nil {
        slog.Debug("elevenlabs: cache hit", "key", cacheKey[:8])
        return data, nil
    }

    // Ensure cache directory exists
    if err := os.MkdirAll(c.cacheDir, 0755); err != nil {
        return nil, fmt.Errorf("elevenlabs: mkdir: %w", err)
    }

    // Call ElevenLabs API
    reqBody, _ := json.Marshal(map[string]any{
        "text":     text,
        "model_id": "eleven_multilingual_v2",
        "voice_settings": map[string]any{
            "stability":        0.5,
            "similarity_boost": 0.75,
        },
    })

    url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", c.VoiceID)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
    if err != nil { return nil, err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("xi-api-key", c.APIKey)
    req.Header.Set("Accept", "audio/mpeg")

    resp, err := c.httpClient.Do(req)
    if err != nil { return nil, fmt.Errorf("elevenlabs API: %w", err) }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("elevenlabs error %d: %s", resp.StatusCode, body)
    }

    data, err := io.ReadAll(resp.Body)
    if err != nil { return nil, err }

    // Save to cache
    if err := os.WriteFile(cachePath, data, 0644); err != nil {
        slog.Warn("elevenlabs: failed to write cache", "err", err)
    }

    return data, nil
}

// FileURL returns the public URL for a synthesized audio file.
// The file may not exist yet — TextToSpeech must be called first.
func (c *Client) FileURL(baseURL, text string) string {
    return baseURL + "/uploads/voice/" + c.sha256hex(text) + ".mp3"
}

func (c *Client) sha256hex(s string) string {
    sum := sha256.Sum256([]byte(s))
    return fmt.Sprintf("%x", sum)
}
```

### `internal/external/webpush/client.go`

Package: `webpush`

```go
package webpush

import (
    "context"
    "fmt"
    "log/slog"

    webpushgo "github.com/SherClockHolmes/webpush-go"
    "github.com/radarul-albinelor/api/internal/domain"
)

// Client sends Web Push notifications.
// Implements cascade.PushSender.
type Client struct {
    VAPIDPublic  string
    VAPIDPrivate string
}

func New(vapidPublic, vapidPrivate string) *Client {
    return &Client{VAPIDPublic: vapidPublic, VAPIDPrivate: vapidPrivate}
}

// Send delivers a push notification to a single subscription.
func (c *Client) Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error {
    if c.VAPIDPublic == "" || c.VAPIDPrivate == "" {
        slog.Warn("[MOCK PUSH] VAPID keys not configured, skipping push")
        return nil
    }

    wsSub := &webpushgo.Subscription{
        Endpoint: sub.Endpoint,
        Keys: webpushgo.Keys{
            Auth:   sub.Auth,
            P256dh: sub.P256dh,
        },
    }

    resp, err := webpushgo.SendNotification(payload, wsSub, &webpushgo.Options{
        VAPIDPublicKey:  c.VAPIDPublic,
        VAPIDPrivateKey: c.VAPIDPrivate,
        Subscriber:      "mailto:noreply@beelive.ro",
        TTL:             3600,
    })
    if err != nil { return fmt.Errorf("webpush send: %w", err) }
    defer resp.Body.Close()

    if resp.StatusCode >= 400 {
        return fmt.Errorf("webpush: status %d", resp.StatusCode)
    }

    slog.Debug("webpush: notification sent", "endpoint", sub.Endpoint[:20])
    return nil
}
```

### `internal/services/pdf.go`

Package: `services`

```go
package services

import (
    "bytes"
    "fmt"
    "time"

    "github.com/jung-kurt/gofpdf"
    "github.com/radarul-albinelor/api/internal/domain"
)

// PDFService generates PDF documents for official notifications.
type PDFService struct{}

func NewPDFService() *PDFService { return &PDFService{} }

// GeneratePrimariePDF creates the official notification PDF for the local municipality (primărie).
func (s *PDFService) GeneratePrimariePDF(
    spray *domain.SprayReport,
    farmer *domain.User,
    parcel *domain.Parcel,
    affectedCount int,
    ledgerHash string,
) ([]byte, error) {
    pdf := gofpdf.New("P", "mm", "A4", "")
    pdf.AddPage()
    pdf.SetFont("Arial", "B", 16)

    // Header — purple color (#4D2B8C = R:77 G:43 B:140)
    pdf.SetTextColor(77, 43, 140)
    pdf.CellFormat(0, 12, "NOTIFICARE OFICIALĂ - TRATAMENT FITOSANITAR", "", 1, "C", false, 0, "")

    pdf.SetTextColor(0, 0, 0)
    pdf.SetFont("Arial", "", 11)
    pdf.Ln(5)

    field := func(label, value string) {
        pdf.SetFont("Arial", "B", 11)
        pdf.Cell(60, 8, label+":")
        pdf.SetFont("Arial", "", 11)
        pdf.CellFormat(0, 8, value, "", 1, "", false, 0, "")
    }

    field("Fermier", farmer.Name)
    field("CNP", "***********"+farmer.CNP[len(farmer.CNP)-2:]) // mask CNP
    field("Număr cadastral parcelă", parcel.CadastralNumber)
    field("Substanță activă", spray.Substance)
    field("Toxicitate", spray.Toxicity)
    field("Data tratamentului", spray.ScheduledAt.Format("02.01.2006 15:04"))
    field("Durata estimată (ore)", fmt.Sprintf("%.1f", spray.DurationHours))
    field("Suprafață (ha)", fmt.Sprintf("%.2f", spray.SurfaceHA))
    field("Cultură", spray.Crop)
    field("Stupine notificate", fmt.Sprintf("%d", affectedCount))
    field("Dată notificare", time.Now().Format("02.01.2006 15:04"))

    pdf.Ln(10)
    pdf.SetFont("Arial", "I", 9)
    pdf.SetTextColor(100, 100, 100)
    pdf.Cell(0, 6, "Hash registru tamper-evident: "+ledgerHash)

    pdf.Ln(4)
    pdf.Cell(0, 6, "Document generat automat de Radarul Albinelor (beelive.ro)")

    var buf bytes.Buffer
    if err := pdf.Output(&buf); err != nil {
        return nil, fmt.Errorf("pdf output: %w", err)
    }
    return buf.Bytes(), nil
}

// GenerateANFExport creates an ANSVSA/ANF export PDF for a farmer's spray history.
func (s *PDFService) GenerateANFExport(
    sprays []domain.SprayReport,
    farmer *domain.User,
    ledgerHash string,
) ([]byte, error) {
    pdf := gofpdf.New("P", "mm", "A4", "")
    pdf.AddPage()
    pdf.SetFont("Arial", "B", 14)
    pdf.SetTextColor(77, 43, 140)
    pdf.CellFormat(0, 10, "REGISTRU TRATAMENTE FITOSANITARE - EXPORT ANF", "", 1, "C", false, 0, "")
    pdf.SetTextColor(0, 0, 0)
    pdf.SetFont("Arial", "", 10)
    pdf.Ln(3)
    pdf.Cell(0, 6, fmt.Sprintf("Fermier: %s | Export: %s", farmer.Name, time.Now().Format("02.01.2006")))
    pdf.Ln(8)

    // Table header
    pdf.SetFont("Arial", "B", 9)
    pdf.SetFillColor(220, 220, 220)
    headers := []struct{text string; w float64}{
        {"Data", 30}, {"Parcelă", 35}, {"Substanță", 45}, {"Toxicitate", 25}, {"Suprafață(ha)", 30}, {"Cultu", 25},
    }
    for _, h := range headers {
        pdf.CellFormat(h.w, 7, h.text, "1", 0, "C", true, 0, "")
    }
    pdf.Ln(-1)

    pdf.SetFont("Arial", "", 9)
    for _, s := range sprays {
        pdf.CellFormat(30, 6, s.ScheduledAt.Format("02.01.06"), "1", 0, "C", false, 0, "")
        pdf.CellFormat(35, 6, s.ParcelID[:8]+"...", "1", 0, "", false, 0, "")
        pdf.CellFormat(45, 6, s.Substance, "1", 0, "", false, 0, "")
        pdf.CellFormat(25, 6, s.Toxicity, "1", 0, "C", false, 0, "")
        pdf.CellFormat(30, 6, fmt.Sprintf("%.2f", s.SurfaceHA), "1", 0, "C", false, 0, "")
        pdf.CellFormat(25, 6, s.Crop, "1", 0, "", false, 0, "")
        pdf.Ln(-1)
    }

    pdf.Ln(5)
    pdf.SetFont("Arial", "I", 8)
    pdf.SetTextColor(100, 100, 100)
    pdf.Cell(0, 5, "Hash registru: "+ledgerHash)

    var buf bytes.Buffer
    if err := pdf.Output(&buf); err != nil { return nil, err }
    return buf.Bytes(), nil
}
```

---

## Files to Modify

### `internal/api/webhooks_twilio.go`

Add Twilio signature validation at the top of each webhook handler.

Create a helper function:
```go
// validateTwilioSignature validates the X-Twilio-Signature header.
// Returns false if validation fails (invalid signature or missing header).
// NOTE: Requires cfg.AppBaseURL to be set to the public HTTPS URL.
func (h *Handlers) validateTwilioSignature(r *http.Request) bool {
    if h.twilioClient == nil {
        // No Twilio client configured — skip validation in dev
        return true
    }
    signature := r.Header.Get("X-Twilio-Signature")
    if signature == "" { return false }

    // Reconstruct the full URL
    fullURL := h.cfg.AppBaseURL + r.URL.Path
    if r.URL.RawQuery != "" { fullURL += "?" + r.URL.RawQuery }

    // Parse form params
    r.ParseForm()
    params := make(map[string]string)
    for k, v := range r.PostForm {
        if len(v) > 0 { params[k] = v[0] }
    }

    return h.twilioClient.ValidateSignature(fullURL, params, signature)
}
```

Add to each raw webhook handler at the top:
```go
if !h.validateTwilioSignature(r) {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

Add `twilioClient *twilio.Client` field to `Handlers`. Wire in `NewRouter`.

**Update `rawVoiceGather`** — serve ElevenLabs TTS audio:

When the handler is called WITHOUT digits (initial call from Twilio), generate TTS and return `<Play>` TwiML:
```go
func (h *Handlers) rawVoiceGather(w http.ResponseWriter, r *http.Request) {
    if !h.validateTwilioSignature(r) {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }
    r.ParseForm()
    callSID := r.FormValue("CallSid")
    digits := r.FormValue("Digits")
    dispatchID := r.URL.Query().Get("dispatch_id")

    if digits != "" {
        // Process confirmed digit
        if digits == "1" && callSID != "" {
            _ = h.cascade.HandleCallConfirmed(r.Context(), callSID)
        }
        w.Header().Set("Content-Type", "application/xml")
        w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say language="ro-RO">Mulțumim! Confirmare înregistrată.</Say></Response>`))
        return
    }

    // Initial call — generate TTS if ElevenLabs configured
    callMsg := buildCallMessage(dispatchID)
    gatherURL := fmt.Sprintf("%s/api/v1/webhooks/twilio/voice/gather?dispatch_id=%s",
        h.cfg.AppBaseURL, dispatchID)

    var twiml string
    if h.elevenLabs != nil && h.cfg.ElevenLabsAPIKey != "" {
        // Pre-generate audio (this blocks briefly but Twilio gives us ~10s)
        _, err := h.elevenLabs.TextToSpeech(r.Context(), callMsg)
        if err != nil {
            slog.Warn("elevenlabs TTS failed, falling back to <Say>", "err", err)
        } else {
            audioURL := h.elevenLabs.FileURL(h.cfg.AppBaseURL, callMsg)
            twiml = fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather numDigits="1" action="%s" method="POST" timeout="10">
    <Play>%s</Play>
  </Gather>
  <Say language="ro-RO">Nu am primit o confirmare. Vă rugăm contactați fermierul direct.</Say>
</Response>`, gatherURL, audioURL)
        }
    }

    if twiml == "" {
        // Fallback to static TwiML with <Say>
        twiml = fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather numDigits="1" action="%s" method="POST" timeout="10">
    <Say language="ro-RO">Atenție! Fermier aplică pesticide în apropierea stupinei dumneavoastră. Apăsați 1 pentru confirmare.</Say>
  </Gather>
  <Say language="ro-RO">Nu am primit o confirmare. Vă rugăm contactați fermierul direct.</Say>
</Response>`, gatherURL)
    }

    w.Header().Set("Content-Type", "application/xml")
    w.Write([]byte(twiml))
}
```

Romanian TTS message builder (add to `webhooks_twilio.go` or `cascade.go`):
```go
// buildCallMessage builds the Romanian TTS message for a dispatch.
// dispatchID is used to look up spray + apiary info.
// For Phase 9, uses a generic message — Phase 10 can personalize.
func buildCallMessage(dispatchID string) string {
    // Simplified for Phase 9 — personalization requires DB lookup
    return "Atenție! Fermier aplică pesticide în apropierea stupinei dumneavoastră. " +
        "Apăsați 1 pentru a confirma primirea acestei alerte."
}

// buildPersonalizedCallMessage creates a detailed message with context.
func buildPersonalizedCallMessage(farmerName, parcelName string, distanceKm float64, apiaryName, scheduledAt, substance, toxicity string) string {
    return fmt.Sprintf(
        "Atenție! Fermierul %s aplică pesticide pe parcela %s la %.1f kilometri de stupina %s, "+
        "programat pentru %s. Substanța utilizată este %s, toxicitate %s. "+
        "Apăsați 1 pentru a confirma primirea acestei alerte.",
        farmerName, parcelName, distanceKm, apiaryName, scheduledAt, substance, toxicity)
}
```

### `internal/api/sprays.go`

**Wire PDF generation and email** into `createSprayReport` after committing the transaction.

After `tx.Commit` and `cascade.Start`:
```go
// Async: generate PDF and send email
go func() {
    bgCtx := context.WithoutCancel(ctx)

    // Get farmer and parcel details for PDF
    farmer, err := h.authSvc.GetUser(bgCtx, user.ID)
    if err != nil {
        slog.Error("pdf: failed to get farmer", "err", err)
        return
    }

    parcelDomain := &domain.Parcel{
        ID:              parcel.ID.String(),
        CadastralNumber: parcel.CadastralNumber,
        Lat:             parcel.Lat,
        Lng:             parcel.Lng,
        SurfaceHA:       parcel.SurfaceHa,
    }

    sprayDomain := &domain.SprayReport{
        ID:            spray.ID.String(),
        Substance:     spray.Substance,
        Toxicity:      domain.Toxicity(spray.Toxicity),
        ScheduledAt:   spray.ScheduledAt,
        DurationHours: spray.DurationHours,
        SurfaceHA:     spray.SurfaceHa,
        Crop:          spray.Crop,
        ParcelID:      spray.ParcelID.String(),
    }

    pdfBytes, err := h.pdfSvc.GeneratePrimariePDF(sprayDomain, farmer, parcelDomain, len(affected), sprayHash)
    if err != nil {
        slog.Error("pdf: generation failed", "err", err)
        return
    }

    // Save PDF to disk
    pdfPath := fmt.Sprintf("uploads/pdfs/%s-primarie.pdf", sprayID.String())
    if err := os.WriteFile(pdfPath, pdfBytes, 0644); err != nil {
        slog.Error("pdf: write failed", "err", err, "path", pdfPath)
        return
    }
    slog.Info("pdf: saved", "path", pdfPath)

    // Ledger event for PDF
    _, _ = h.ledgerSvc.Append(bgCtx, nil, "pdf.generated", &user.ID, map[string]any{
        "spray_id": sprayID.String(), "path": pdfPath,
    })

    // Send email with PDF attachment
    if h.emailClient != nil {
        subject := fmt.Sprintf("Notificare tratament fitosanitar - %s", spray.ScheduledAt.Format("02.01.2006"))
        body := fmt.Sprintf("Bună ziua,\n\nAtașat găsiți notificarea oficială pentru tratamentul fitosanitar programat de %s.\n\nSistemul Radarul Albinelor", farmer.Name)

        // For now: send to primarie email (simple text email)
        // Phase 9 enhancement: attach PDF bytes as email attachment
        if err := h.emailClient.Send(bgCtx, h.cfg.PrimarieEmail, subject, body); err != nil {
            slog.Error("email: failed to send", "err", err)
        } else {
            slog.Info("email: sent to primarie")
            _, _ = h.ledgerSvc.Append(bgCtx, nil, "email.sent", &user.ID, map[string]any{
                "spray_id": sprayID.String(), "to": h.cfg.PrimarieEmail,
            })
        }
    }
}()
```

**`GET /spray-reports/:id/primarie-pdf`** — serve the PDF file:
```go
func (h *Handlers) getSprayPDF(w http.ResponseWriter, r *http.Request) {
    sprayID := chi.URLParam(r, "id")
    pdfPath := fmt.Sprintf("uploads/pdfs/%s-primarie.pdf", sprayID)
    w.Header().Set("Content-Type", "application/pdf")
    w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-primarie.pdf"`, sprayID[:8]))
    http.ServeFile(w, r, pdfPath)
}
```
Register as raw chi route (not Huma) to handle the binary response cleanly.

### `internal/api/apiaries.go`

Implement `POST /apiaries` fully:

```go
type CreateApiaryInput struct {
    Body struct {
        Name      string  `json:"name" minLength:"1"`
        Type      string  `json:"type" enum:"permanent,pastoral"`
        Lat       float64 `json:"lat"`
        Lng       float64 `json:"lng"`
        HiveCount int     `json:"hive_count" minimum:"0"`
        StartDate string  `json:"start_date"` // YYYY-MM-DD
        Notes     *string `json:"notes,omitempty"`
    }
}

func (h *Handlers) createApiary(ctx context.Context, input *CreateApiaryInput) (*CreateApiaryOutput, error) {
    user := middleware.UserFromContext(ctx)
    if user.Role != domain.RoleApicultor { return nil, huma.NewError(403, "only beekeepers") }

    ownerID, _ := uuid.Parse(user.ID)
    startDate, err := time.Parse("2006-01-02", input.Body.StartDate)
    if err != nil { return nil, huma.NewError(400, "invalid start_date, use YYYY-MM-DD") }

    tx, err := h.pool.Begin(ctx)
    if err != nil { return nil, err }
    defer tx.Rollback(ctx)

    qtx := dbsqlc.New(tx)
    apiary, err := qtx.CreateApiary(ctx, dbsqlc.CreateApiaryParams{
        ID:        uuid.New(),
        OwnerID:   ownerID,
        Name:      input.Body.Name,
        Type:      dbsqlc.ApiaryType(input.Body.Type),
        Lat:       input.Body.Lat,
        Lng:       input.Body.Lng,
        HiveCount: int32(input.Body.HiveCount),
        StartDate: startDate,
        EndDate:   sql.NullTime{},
        Notes:     sql.NullString{String: ptrStr(input.Body.Notes), Valid: input.Body.Notes != nil},
    })
    if err != nil { return nil, fmt.Errorf("create apiary: %w", err) }

    actorID := user.ID
    ledgerHash, err := h.ledgerSvc.Append(ctx, tx, "apiary.created", &actorID, map[string]any{
        "apiary_id": apiary.ID.String(), "name": apiary.Name, "type": input.Body.Type,
    })
    if err != nil { return nil, err }

    _ = qtx.UpdateApiaryLedgerHash(ctx, dbsqlc.UpdateApiaryLedgerHashParams{
        ID: apiary.ID, LedgerHash: ledgerHash,
    })

    if err := tx.Commit(ctx); err != nil { return nil, err }

    // Async: if pastoral type, generate PDF and email
    if input.Body.Type == "pastoral" {
        go func() {
            bgCtx := context.WithoutCancel(ctx)
            farmer, err := h.authSvc.GetUser(bgCtx, user.ID)
            if err != nil { slog.Error("pastoral pdf: get user failed", "err", err); return }
            // Simplified pastoral notification email
            subject := fmt.Sprintf("Notificare amplasare stupină pastorală - %s", apiary.Name)
            body := fmt.Sprintf("Stupina pastorală '%s' a fost înregistrată la coordonatele (%.4f, %.4f).\n\nFermier: %s",
                apiary.Name, apiary.Lat, apiary.Lng, farmer.Name)
            if h.emailClient != nil {
                _ = h.emailClient.Send(bgCtx, h.cfg.PrimarieEmail, subject, body)
            }
        }()
    }

    resp := dbApiaryToResponse(apiary)
    resp.LastLedgerHash = ledgerHash
    return &CreateApiaryOutput{Body: struct{Apiary ApiaryResponse `json:"apiary"`}{resp}}, nil
}
```

### `internal/api/router.go`

Updated `Handlers` struct (final after Phase 9):
```go
type Handlers struct {
    cfg           *config.Config
    pool          *pgxpool.Pool
    jwt           *platform.JWTService
    authSvc       *services.AuthService
    ledgerSvc     *services.LedgerService
    geoAI         geoai.Client
    weatherClient *weather.CachedClient
    cascade       *services.CascadeService
    twilioClient  *twilio.Client      // nil if Twilio not configured
    elevenLabs    *elevenlabs.Client  // nil if ElevenLabs not configured
    pushClient    *webpush.Client     // nil if VAPID not configured
    emailClient   *email.EmailClient  // always set (mock-safe)
    pdfSvc        *services.PDFService
}
```

In `NewRouter`:
```go
// Twilio client (nil if not configured)
var twilioClient *twilio.Client
if cfg.TwilioAccountSID != "" {
    twilioClient = twilio.New(cfg.TwilioAccountSID, cfg.TwilioAuthToken, cfg.TwilioFromPhone)
}

// ElevenLabs client (nil if not configured)
var elClient *elevenlabs.Client
if cfg.ElevenLabsAPIKey != "" {
    elClient = elevenlabs.New(cfg.ElevenLabsAPIKey, cfg.ElevenLabsVoiceID, "uploads/voice")
}

// Web push client (nil if not configured)
var pushClient *webpush.Client
if cfg.VAPIDPublicKey != "" && cfg.VAPIDPrivateKey != "" {
    pushClient = webpush.New(cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey)
}

// PDF service
pdfSvc := services.NewPDFService()

// Cascade with real clients (nil-safe — cascade handles nil gracefully)
var notifier services.Notifier
if twilioClient != nil { notifier = twilioClient }
var pusher services.PushSender
if pushClient != nil { pusher = pushClient }

cascadeSvc := services.NewCascadeService(pool, ledgerSvc, notifier, pusher, cfg.AppBaseURL)

h := &Handlers{
    cfg: cfg, pool: pool, jwt: jwtSvc,
    authSvc: authSvc, ledgerSvc: ledgerSvc,
    geoAI: geoAIClient, weatherClient: weatherClient,
    cascade: cascadeSvc,
    twilioClient: twilioClient, elevenLabs: elClient,
    pushClient: pushClient, emailClient: emailClient,
    pdfSvc: pdfSvc,
}
```

Mount static file server for uploads:
```go
r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))
```

Register raw route for PDF download:
```go
r.Get("/api/v1/spray-reports/{id}/primarie-pdf", h.getSprayPDF)
```

### `cmd/server/main.go`

Ensure upload directories exist on startup:
```go
// After config load, before DB connection:
for _, dir := range []string{"uploads/pdfs", "uploads/voice"} {
    if err := os.MkdirAll(dir, 0755); err != nil {
        slog.Error("failed to create upload dir", "dir", dir, "err", err)
        os.Exit(1)
    }
}
```

---

## Key Implementation Details

1. **`twilio.Client` implements `cascade.Notifier`**: The `SendSMS` and `MakeCall` methods match the interface. Assign as `var notifier services.Notifier = twilioClient` — Go will check interface satisfaction at compile time.

2. **`webpush.Client` implements `cascade.PushSender`**: The `Send(ctx, sub, payload)` method matches. Assign as `var pusher services.PushSender = pushClient`.

3. **Nil guard pattern in cascade**: All places in `cascade.go` that call `c.notifier` and `c.pusher` already check `if c.notifier != nil`. This was established in Phase 8. No changes needed to cascade.go itself — just pass the real clients to `NewCascadeService`.

4. **ElevenLabs file serving**: Files are saved to `uploads/voice/<sha256>.mp3`. The file server mounted at `/uploads/*` serves them. Twilio fetches the audio URL when processing the call. The URL must be publicly accessible — requires a tunnel (Phase 11).

5. **`gofpdf` encoding**: The library uses Latin-1 by default. For Romanian characters (ș, ț, ă, â, î), use ASCII approximations or enable UTF-8 via the `translator` option. Simple workaround: use ASCII equivalents in the PDF text (s, t, a, a, i) or call `pdf.UnicodeTranslatorFromDescriptor("")`. For hackathon, transliterate Romanian chars:
   ```go
   // Add to PDFService
   func romanize(s string) string {
       replacer := strings.NewReplacer("ș", "s", "ț", "t", "ă", "a", "â", "a", "î", "i", "Ș", "S", "Ț", "T", "Ă", "A", "Â", "A", "Î", "I")
       return replacer.Replace(s)
   }
   ```
   Apply `romanize()` to all PDF text fields.

6. **Twilio HMAC-SHA1 signature**: The exact algorithm from Twilio docs:
   - Sort POST params by key name alphabetically
   - Concatenate: `url + key1value1 + key2value2 + ...` (no separators)
   - HMAC-SHA1 with AuthToken as key
   - Base64 encode
   - Compare to `X-Twilio-Signature` header
   This is what `twilio.Client.ValidateSignature` implements.

7. **Skip signature validation in dev**: If `twilioClient == nil` (Twilio not configured), `validateTwilioSignature` returns `true`. This allows testing webhooks locally without a real Twilio account.

8. **PDF email attachment**: The `go-mail` library supports attachments via `msg.AttachFile(path)` or `msg.AttachReader(name, reader, contentType)`. For Phase 9, send a simple text email without attachment (simpler implementation). Phase 10/11 can add the PDF as attachment:
   ```go
   // Advanced: attach PDF
   msg.AttachReader("notificare.pdf", bytes.NewReader(pdfBytes), "application/pdf")
   ```
   Actually implement the attachment in Phase 9 since the library supports it. Modify `email.EmailClient.Send` to also accept optional attachment bytes:
   ```go
   func (c *EmailClient) SendWithAttachment(ctx context.Context, to, subject, body string, attachName string, attachData []byte) error
   ```

9. **`ElevenLabsVoiceID` default**: Config has `envDefault:"21m00Tcm4TlvDq8ikWAM"` which is the "Rachel" voice (English). For Romanian, use a Romanian voice ID. Look up ElevenLabs for a Romanian voice — the user will need to set `ELEVENLABS_VOICE_ID` to a Romanian voice ID in their ElevenLabs account.

10. **`webpush-go` SendNotification is synchronous**: It makes an HTTP request. Call it in a goroutine if needed. In `cascade.sendPushForDispatch`, already called in a goroutine context.

---

## Verification Steps

```bash
# Build check
go build ./...
go vet ./...

# Start server with real credentials in .env
# cp .env.example .env && edit .env with real keys
go run ./cmd/server &

# 1. Verify uploads directories created
ls uploads/
# Expected: pdfs/ voice/

# 2. POST spray report (triggers real Twilio call if configured)
# Login as fermier first...
SPRAY=$(curl -s -X POST http://localhost:8080/api/v1/spray-reports \
  -H 'Content-Type: application/json' \
  -b /tmp/farmer_cookies.txt \
  -d '{"parcel_id":"...","surface_ha":5.2,"crop":"rapița","substance":"Confidor Energy","scheduled_at":"2026-06-01T09:00:00Z","duration_hours":2}')
SPRAY_ID=$(echo "$SPRAY" | jq -r '.spray_report.id')

# 3. Check PDF was generated asynchronously (wait ~2 seconds)
sleep 2
ls uploads/pdfs/
# Expected: {sprayID}-primarie.pdf exists

# 4. Download PDF
curl -s -o /tmp/test.pdf "http://localhost:8080/api/v1/spray-reports/$SPRAY_ID/primarie-pdf" \
  -b /tmp/farmer_cookies.txt
file /tmp/test.pdf
# Expected: PDF document

# 5. Check ElevenLabs cache (if configured)
ls uploads/voice/
# Expected: one or more .mp3 files after a call is made

# 6. Twilio signature validation
# Valid request (pass real Twilio signature):
# (simulate with real Twilio webhook — or test with ngrok/tunnel)

# Invalid signature → 403
curl -s -X POST http://localhost:8080/api/v1/webhooks/twilio/voice/gather \
  -H 'X-Twilio-Signature: invalid' \
  -d 'CallSid=CATEST' | cat
# Expected: 403 forbidden (if TwilioAccountSID is configured)

# 7. Web push test (requires browser subscription)
# POST /push/subscriptions with real browser subscription object
# Then trigger spray report → check browser notification

# 8. POST /apiaries (new endpoint)
curl -s -X POST http://localhost:8080/api/v1/apiaries \
  -H 'Content-Type: application/json' \
  -b /tmp/beekeeper_cookies.txt \
  -d '{"name":"Stupina Noua","type":"permanent","lat":46.784,"lng":23.612,"hive_count":10,"start_date":"2026-01-01"}' | jq .
# Expected: apiary created with ledger_hash

# 9. Ledger still valid
curl -s http://localhost:8080/api/v1/events/verify -b /tmp/beekeeper_cookies.txt | jq .valid
# Expected: true
```

---

## After Completion

Write `/Users/alinscreciu/work/hackathon/api/.planning/phases/phase-9.md`:

```markdown
# Phase 9 — External Integrations
Status: COMPLETE
Completed: <date>

## What was built
- internal/external/twilio/client.go — SendSMS, MakeCall, ValidateSignature
- internal/external/elevenlabs/client.go — TextToSpeech with disk cache
- internal/external/webpush/client.go — Send via webpush-go
- internal/services/pdf.go — GeneratePrimariePDF, GenerateANFExport (gofpdf)
- internal/api/webhooks_twilio.go — Twilio signature validation, ElevenLabs TTS in voice gather
- internal/api/sprays.go — async PDF generation + email after spray creation, GET /primarie-pdf
- internal/api/apiaries.go — POST /apiaries fully implemented
- internal/api/router.go — real clients wired, /uploads/* served, nil guards for optional services
- cmd/server/main.go — uploads/ dirs created on startup

## Verification
- Real Twilio call initiated on spray report (if configured)
- ElevenLabs MP3 cached in uploads/voice/
- PDF saved in uploads/pdfs/{id}-primarie.pdf
- GET /primarie-pdf serves PDF
- Invalid Twilio signature → 403
- go build ./... clean
```
