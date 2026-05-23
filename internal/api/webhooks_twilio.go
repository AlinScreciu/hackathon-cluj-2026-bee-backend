package api

import (
	"log/slog"
	"net/http"
	"strings"
)

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

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")

	if digits == "1" && callSID != "" {
		if err := h.cascade.HandleCallConfirmed(r.Context(), callSID); err != nil {
			slog.Error("twilio voice gather: handle confirmed", "err", err, "call_sid", callSID)
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say language="ro-RO">Mulțumim! Confirmare înregistrată.</Say></Response>`))
		return
	}

	if digits != "" && callSID != "" {
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say language="ro-RO">Nu am primit o confirmare validă. Vă rugăm contactați fermierul direct.</Say></Response>`))
		return
	}

	// Initial connection — serve the gather TwiML.
	alertTextRO := "Atenție! Un fermier aplică pesticide în apropierea stupinei dumneavoastră. Apăsați 1 pentru confirmare."
	sayTextASCII := "Atentie! Un fermier aplica pesticide in apropierea stupinei dumneavoastra. Apasati 1 pentru confirmare."

	gatherAction := h.cfg.AppBaseURL + "/api/v1/webhooks/twilio/voice/gather"

	var innerXML string
	if h.elevenLabs != nil {
		if _, err := h.elevenLabs.TextToSpeech(r.Context(), alertTextRO); err != nil {
			slog.Warn("elevenlabs TTS failed, using Say fallback", "err", err)
			innerXML = `<Say language="ro-RO">` + sayTextASCII + `</Say>`
		} else {
			audioURL := h.elevenLabs.FileURL(h.cfg.AppBaseURL, alertTextRO)
			innerXML = `<Play>` + audioURL + `</Play>`
		}
	} else {
		innerXML = `<Say language="ro-RO">` + sayTextASCII + `</Say>`
	}

	twiml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  ` + innerXML + `
  <Gather numDigits="1" action="` + gatherAction + `" method="POST" timeout="10">
  </Gather>
  <Say language="ro-RO">Nu am primit o confirmare. Va rugam contactati fermierul direct.</Say>
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
	sig := r.Header.Get("X-Twilio-Signature")
	fullURL := h.cfg.AppBaseURL + r.URL.RequestURI()
	return h.twilioClient.ValidateSignature(fullURL, r.Form, sig)
}
