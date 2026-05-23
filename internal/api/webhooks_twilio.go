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
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say language="ro-RO">Atenție! Un fermier aplică pesticide în apropierea stupinei dumneavoastră. Apăsați 1 pentru confirmare.</Say>
  <Gather numDigits="1" action="/api/v1/webhooks/twilio/voice/gather" method="POST" timeout="10">
  </Gather>
  <Say language="ro-RO">Nu am primit o confirmare. Vă rugăm contactați fermierul direct.</Say>
</Response>`))
}

// rawVoiceStatus handles Twilio call status callbacks.
func (h *Handlers) rawVoiceStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusNoContent)
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

	smsSID := r.FormValue("SmsSid")
	status := r.FormValue("MessageStatus")

	if smsSID != "" && status != "" {
		if err := h.cascade.HandleSMSStatus(r.Context(), smsSID, status); err != nil {
			slog.Error("twilio sms status: update state", "err", err, "sms_sid", smsSID, "status", status)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
