package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"

	dbsqlc "github.com/radarul-albinelor/api/internal/db/sqlc"
)

var asciiTwilioReplacer = strings.NewReplacer(
	"ă", "a", "â", "a", "î", "i", "ș", "s", "ț", "t",
	"Ă", "A", "Â", "A", "Î", "I", "Ș", "S", "Ț", "T",
)

// xmlTextEscaper escapes characters that would otherwise break a TwiML
// document. R2 presigned URLs contain unescaped `&` between query params,
// which the XML parser treats as the start of an entity reference.
var xmlTextEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// playOrSay returns a TwiML verb that prefers an ElevenLabs <Play> over Twilio's <Say>.
// On any ElevenLabs error it falls back to <Say> with diacritics stripped for safety.
func (h *Handlers) playOrSay(ctx context.Context, text string) string {
	if h.elevenLabs != nil {
		if audioURL, err := h.elevenLabs.TextToSpeechURL(ctx, text); err == nil {
			return `<Play>` + xmlTextEscaper.Replace(audioURL) + `</Play>`
		} else {
			slog.Error("elevenlabs TTS failed, using Say fallback", "err", err)
		}
	}
	return `<Say language="ro-RO">` + asciiTwilioReplacer.Replace(text) + `</Say>`
}

// rawVoiceGather handles Twilio voice gather callbacks.
// Called with no Digits on initial connection (serves TwiML) and with Digits
// after DTMF collection.
func (h *Handlers) rawVoiceGather(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !h.validateTwilioSignature(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	callSID := r.FormValue("CallSid")
	digits := r.FormValue("Digits")
	dispatchIDStr := r.URL.Query().Get("dispatch_id")

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")

	if digits == "1" && callSID != "" {
		if err := h.cascade.HandleCallConfirmed(r.Context(), callSID); err != nil {
			slog.Error("twilio voice gather: handle confirmed", "err", err, "call_sid", callSID)
		}
		confirmVerb := h.playOrSay(r.Context(), "Mulțumim! Confirmare înregistrată. La revedere.")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response>` + confirmVerb + `</Response>`))
		return
	}

	if digits != "" && callSID != "" {
		invalidVerb := h.playOrSay(r.Context(), "Nu am primit o confirmare validă. Vă rugăm verificați SMS-ul primit.")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response>` + invalidVerb + `</Response>`))
		return
	}

	// Initial connection — build dynamic alert text from dispatch data.
	alertTextRO := h.buildVoiceAlertText(r, dispatchIDStr)
	alertVerb := h.playOrSay(r.Context(), alertTextRO)
	timeoutVerb := h.playOrSay(r.Context(), "Nu am primit o confirmare. Vă rugăm verificați SMS-ul primit.")

	gatherAction := h.cfg.AppBaseURL + "/api/v1/webhooks/twilio/voice/gather"

	twiml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  ` + alertVerb + `
  <Gather numDigits="1" action="` + gatherAction + `" method="POST" timeout="10">
  </Gather>
  ` + timeoutVerb + `
</Response>`
	_, _ = w.Write([]byte(twiml))
}

// rawVoiceStatus handles Twilio call status callbacks.
func (h *Handlers) rawVoiceStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !h.validateTwilioSignature(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	callSID := r.FormValue("CallSid")
	callStatus := r.FormValue("CallStatus")

	if callSID != "" && callStatus != "" {
		switch callStatus {
		case "no-answer", "busy", "failed", "canceled":
			if err := h.cascade.HandleCallTerminal(r.Context(), callSID, callStatus); err != nil {
				slog.Error("twilio voice status: handle terminal", "err", err, "call_sid", callSID, "status", callStatus)
			}
		case "completed":
			// Confirmation is handled by HandleCallConfirmed via the gather webhook.
			slog.Debug("twilio voice status: call completed", "call_sid", callSID)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// rawSMSInbound handles inbound SMS messages. Beekeeper replies "DA" to confirm.
func (h *Handlers) rawSMSInbound(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
		return
	}

	if !h.validateTwilioSignature(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	smsSID := r.FormValue("SmsSid")
	body := strings.ToUpper(strings.TrimSpace(r.FormValue("Body")))

	if smsSID != "" && strings.Contains(body, "DA") {
		if err := h.cascade.HandleSMSConfirmed(r.Context(), smsSID); err != nil {
			slog.Error("twilio sms inbound: handle confirmed", "err", err, "sms_sid", smsSID)
		}
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
}

// rawSMSStatus handles Twilio SMS delivery status callbacks.
func (h *Handlers) rawSMSStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !h.validateTwilioSignature(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	smsSID := r.FormValue("SmsSid")
	status := r.FormValue("MessageStatus")

	if smsSID != "" && status != "" {
		if err := h.cascade.HandleSMSStatus(r.Context(), smsSID, status); err != nil {
			slog.Error("twilio sms status: update state", "err", err, "sms_sid", smsSID, "status", status)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) validateTwilioSignature(r *http.Request) bool {
	if h.twilioClient == nil {
		return true
	}
	// Skip signature validation in non-production — cloudflared tunnels rewrite
	// the Host header, which breaks Twilio's HMAC check.
	if h.cfg.AppEnv != "production" {
		return true
	}
	sig := r.Header.Get("X-Twilio-Signature")
	fullURL := h.cfg.AppBaseURL + r.URL.RequestURI()
	// Twilio's HMAC is over URL + sorted-concat of POST-body params only. URL
	// query params are already baked into the URL portion of the HMAC; passing
	// r.Form (which is URL query ∪ body) would double-count them and cause a
	// signature mismatch on any webhook whose URL has a query string (e.g.
	// .../voice/gather?dispatch_id=...).
	return h.twilioClient.ValidateSignature(fullURL, r.PostForm, sig)
}

// buildVoiceAlertText looks up the dispatch, beekeeper, apiary and spray report
// to produce a personalised Romanian alert message for the ElevenLabs TTS call.
func (h *Handlers) buildVoiceAlertText(r *http.Request, dispatchIDStr string) string {
	fallback := "Atenție! Un fermier aplică pesticide în apropierea stupinei dumneavoastră. Apăsați 1 pentru confirmare."
	if h.db == nil || dispatchIDStr == "" {
		return fallback
	}

	dispatchID, err := uuid.Parse(dispatchIDStr)
	if err != nil {
		return fallback
	}

	ctx := r.Context()

	dispatch, err := h.db.GetAlertDispatch(ctx, dispatchID)
	if err != nil {
		slog.Warn("buildVoiceAlertText: get dispatch", "err", err)
		return fallback
	}

	beekeeper, err := h.db.GetUserByID(ctx, dispatch.BeekeeperID)
	if err != nil {
		slog.Warn("buildVoiceAlertText: get beekeeper", "err", err)
		return fallback
	}

	spray, err := h.db.GetSprayReport(ctx, dispatch.SprayReportID)
	if err != nil {
		slog.Warn("buildVoiceAlertText: get spray report", "err", err)
		return fallback
	}

	// List every affected apiary the beekeeper owns for this spray, so
	// multi-apiary beekeepers hear all distances in one call.
	siblings, err := h.db.ListDispatchSiblings(ctx, dbsqlc.ListDispatchSiblingsParams{
		SprayReportID: dispatch.SprayReportID,
		BeekeeperID:   dispatch.BeekeeperID,
	})
	if err != nil || len(siblings) == 0 {
		slog.Warn("buildVoiceAlertText: list siblings, falling back to primary only", "err", err)
		siblings = []dbsqlc.AlertDispatch{dispatch}
	}

	apiaryClause := h.buildVoiceApiaryClause(ctx, siblings)
	firstName := strings.Fields(beekeeper.FullName)[0]

	return fmt.Sprintf(
		"%s, alertă Bi-Liv! "+
			"Un fermier va aplica %s %s pe %s. "+
			"Protejați stupii. Apăsați 1 pentru confirmare.",
		firstName,
		spray.Substance,
		apiaryClause,
		spray.ScheduledAt.Format("2 ianuarie"),
	)
}

// buildVoiceApiaryClause produces a Romanian phrase listing every affected
// apiary and its distance, used inside the voice TTS template.
func (h *Handlers) buildVoiceApiaryClause(ctx context.Context, dispatches []dbsqlc.AlertDispatch) string {
	parts := make([]string, 0, len(dispatches))
	for i, d := range dispatches {
		apiary, err := h.db.GetApiary(ctx, d.ApiaryID)
		if err != nil {
			slog.Warn("buildVoiceApiaryClause: get apiary", "apiary_id", d.ApiaryID, "err", err)
			continue
		}
		km := math.Round(d.DistanceM/100) / 10
		if i == 0 {
			parts = append(parts, fmt.Sprintf("la %.1f km de %s", km, apiary.Name))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f km de %s", km, apiary.Name))
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " și " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " și " + parts[len(parts)-1]
	}
}
