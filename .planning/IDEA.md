# Radarul Albinelor — Product Concept

## What It Is

"Radarul Albinelor" (Bee Radar) is a Romanian government-grade notification platform that protects honeybee colonies from pesticide exposure. When a farmer schedules a pesticide spray, the system automatically identifies all registered beekeepers within the hazard radius and notifies them through a redundant multi-channel cascade before the spray begins.

Beekeepers get enough warning to move their hives or seal them in place, preventing colony loss. The entire notification chain — from spray report to each beekeeper's acknowledgement — is recorded in a tamper-evident hash-chain ledger that can be produced as legal evidence in damage disputes.

---

## Why It Matters

Pesticide kills are the second largest cause of honeybee colony collapse in Romania, after Varroa mite. Romanian law (OG 4/1995 and subsequent MADR regulations) requires farmers to notify beekeepers before spraying, but the notification mechanism is informal — a phone call, or nothing. There is no audit trail, no enforcement, and no fallback if the beekeeper misses the call.

Radarul Albinelor automates and formalises this obligation:
- Farmers cannot claim they notified beekeepers if the ledger shows no dispatch.
- Beekeepers cannot claim ignorance if the ledger shows confirmed delivery.
- Inspectors have a neutral, cryptographically verifiable record for adjudication.

Built for **Cluj Hackathon 2026** — designed to be demonstrable end-to-end in under 10 minutes using a live Twilio account, real voice synthesis in Romanian, and a cloudflared tunnel.

---

## The Three User Roles

### Apicultor (Beekeeper)
Registers their apiaries (GPS location, hive count, seasonal dates). Receives automated alerts when a spray is planned near their hives. Confirms receipt via voice call (press 1), SMS reply ("DA"), or the web app. Can file a damage claim with photos if their colony is harmed despite notification.

**Primary concern:** Know in time to act. Move hives or seal entrances before the spray begins.

### Fermier (Farmer)
Registers their agricultural parcels (GPS, cadastral number, crop type). Files spray reports: substance, toxicity level, scheduled date/time, duration, surface. The system does all geo calculations and notification dispatching automatically. Receives a legally-valid PDF report proving they fulfilled their notification obligation.

**Primary concern:** Comply with the law without manually tracking down every beekeeper in a 2 km radius.

### Inspector (County Inspector)
Read-only role. Views a map of all spray reports and affected apiaries in their county. Can drill into individual spray events to see the notification cascade status for each beekeeper. Uses the ledger verification endpoint to confirm the hash chain is intact. The inspector's view is the legal audit surface.

**Primary concern:** Adjudicate disputes with a neutral, tamper-evident record.

---

## The Notification Cascade

The cascade is the core of the product. For each beekeeper who has an apiary within the affected radius:

```
Spray report filed
       │
       ▼
1. Web Push notification (immediate, if browser subscribed)
       │
       ▼ (parallel with push)
2. Voice Call via Twilio
   ├── ElevenLabs synthesises Romanian TTS audio ("Atenție! Fermierul X
   │   va efectua o tratare pe parcela Y la ora Z. Apăsați 1 pentru confirmare.")
   ├── Twilio reads the MP3 via TwiML <Play> + <Gather>
   └── Beekeeper presses 1 → confirmed, timer cancelled
       │
       │ (if no press-1 within 60 seconds)
       ▼
3. SMS fallback ("DA pentru confirmare")
   └── Beekeeper replies "DA" → confirmed

Exception — T- (low toxicity):
   Skip voice call entirely. Send push + SMS simultaneously as co-primary.
   SMS is NOT a fallback for T-; it fires immediately alongside push.
```

Each state transition (push sent, call placed, call confirmed, SMS sent, SMS confirmed) is appended to the ledger.

---

## The Hash-Chain Ledger

Every significant event is stored in `ledger_events` as an append-only log. Each row contains:
- `hash` — SHA256 of (prev_hash + event_type + actor_id + payload JSON + timestamp)
- `prev_hash` — hash of the previous row (NULL for genesis)
- `type` — e.g. `spray_report_created`, `alert_dispatched`, `call_confirmed`, `damage_claim_filed`
- `payload` — JSON blob of event-specific data

The chain can be verified at any time by re-computing all hashes from the genesis row forward. If any row has been tampered with, verification fails.

A Postgres advisory lock (`pg_advisory_xact_lock(42)`) prevents two concurrent transactions from reading the same chain tip and producing duplicate `prev_hash` values.

The ledger is legal infrastructure: it produces an unambiguous, independently verifiable record of who was notified, when, and whether they confirmed.

---

## Damage Claims

If a beekeeper suffers colony loss despite (or because of lack of) notification, they can file a damage claim through the app:
- Linked to the specific spray report
- Photos of dead bees / damaged hives uploaded (stored in `uploads/photos/`)
- Inspector reviews the claim against the ledger record
- Status transitions: `filed → under_review → accepted | rejected`

Each status change is recorded in the ledger.

---

## Hackathon Context

The system is designed to be demoed live:
- `make demo-spray` runs an automated shell script: logs in as a farmer, files a spray report, then polls the cascade status endpoint while Twilio calls the beekeeper phone number.
- All demo users are seeded with `make seed`.
- The cloudflared tunnel (`make tunnel`) allows Twilio to reach localhost for webhook callbacks.
- ElevenLabs TTS produces genuine Romanian speech with correct diacritics.

The architecture is production-grade (advisory locks, context cancellation safety, panic recovery, structured logging) because the hackathon judges include government procurement officers evaluating real deployability.
