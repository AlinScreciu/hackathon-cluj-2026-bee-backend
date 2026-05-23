# Radarul Albinelor — API Contract v1

**Source of truth.** Both the Go API and the Next.js frontend implement against this document. When Huma exports `openapi.json` from the running API, it must match this contract. If they disagree, this document wins — fix the code.

This is a living doc during the hackathon: any change is opened as a PR comment / Slack thread and merged into this file before either side ships. Do not surprise the other team.

---

## 0. Conventions

**Base URL (dev):** `http://localhost:8080`
**Base URL (deployed):** TBD
**All paths prefixed:** `/api/v1`
**Content-Type:** `application/json` (except PDF endpoints → `application/pdf`)
**Timestamps:** RFC 3339 / ISO 8601 in UTC, e.g. `2026-05-23T18:04:42Z`
**IDs:** UUIDv7 (sortable, time-ordered) as lowercase strings
**Pagination (where applicable):** `?limit=20&cursor=<opaque>` → response includes `next_cursor` (null if end)
**Localization:** all user-facing strings in Romanian; field names and enums in English

### Auth
- Session is a JWT in an **HTTP-only, Secure, SameSite=Lax cookie** named `ra_session`
- Lifetime: 24h sliding
- The cookie is set by `POST /auth/2fa/verify` and cleared by `POST /auth/logout`
- Frontend never touches the token directly; it relies on the cookie being included automatically
- For role-protected endpoints, API returns `403` with `code: "forbidden_role"` if user is wrong role

### Error model
Every non-2xx response has this shape:

```ts
{
  error: {
    code: string,           // machine-readable, e.g. "validation_failed", "not_found"
    message: string,        // human-readable, in Romanian
    details?: object | null // optional, e.g. field-level validation errors
  }
}
```

Common codes:
- `validation_failed` (400) — `details` is `{ field_name: "reason in Romanian" }`
- `unauthorized` (401) — no session or expired
- `forbidden_role` (403) — wrong role for this resource
- `not_found` (404)
- `conflict` (409) — e.g., duplicate CNP on register
- `rate_limited` (429) — `details.retry_after_seconds`
- `internal` (500)

### Roles enum
`apicultor` | `fermier` | `inspector`

### Toxicity enum
`T-` | `T` | `T+`
- `T-`: low. Risk radius 750m. Skip call cascade — only push + SMS.
- `T`: medium. Risk radius 1500m. Full cascade.
- `T+`: high. Risk radius 3000m. Full cascade, urgent priority.

### Substance → toxicity lookup
Maintained server-side. Provide via `GET /reference/substances` (see §10).

---

## 1. Authentication

### `POST /auth/login`
Body:
```ts
{ cnp: string, password: string }
```
Validation: CNP is 13 digits.

Response 200:
```ts
{ challenge_id: string, method: "push" | "sms" | "email", masked_destination: string }
```
`masked_destination` is e.g. `"+40 7•• ••• •42"`.

Response 401: `{ code: "invalid_credentials" }`.

The frontend then shows the 2FA screen. The backend has sent a 6-digit code via the selected method (mock provider for `push`, real Twilio SMS for `sms`, SendGrid for `email`).

### `POST /auth/2fa/method`
Body: `{ challenge_id: string, method: "push" | "sms" | "email" }`
Lets the user switch 2FA channel. Response 200: same shape as login response.

### `POST /auth/2fa/verify`
Body: `{ challenge_id: string, code: string }`
Response 200: sets `ra_session` cookie. Body:
```ts
{ user: User }
```
Response 401: `{ code: "invalid_2fa_code" }`.

### `POST /auth/logout`
Clears the cookie. Response 204.

### `GET /auth/me`
Response 200: `{ user: User }`.
Response 401 if no session.

```ts
type User = {
  id: string
  cnp: string                 // returned only for the user themself
  name: string                // "Maria P."
  full_name: string
  email: string
  phone: string               // E.164, e.g. "+40712345678"
  role: "apicultor" | "fermier" | "inspector"
  county: string              // "Cluj"
  locality: string            // "Apahida"
}
```

---

## 2. Apiaries (Stupine) — `apicultor` only

### `GET /apiaries`
Response 200: `{ items: Apiary[] }`

### `POST /apiaries`
Body:
```ts
{
  name: string
  type: "permanent" | "pastoral"
  lat: number
  lng: number
  hive_count: number
  start_date: string        // ISO date; for pastoral, expected duration
  end_date?: string         // for pastoral
  notes?: string
}
```
Side effects: writes `apiary.registered` ledger event; if `pastoral`, also generates a PDF for the local primărie and emails it.
Response 201: `{ apiary: Apiary, ledger_hash: string }`.

### `GET /apiaries/:id`
Response 200: `{ apiary: Apiary, history: LedgerEventSummary[] }`.

### `PATCH /apiaries/:id`
Body: partial Apiary. Triggers `apiary.updated` ledger event.

```ts
type Apiary = {
  id: string
  owner_id: string
  name: string
  type: "permanent" | "pastoral"
  lat: number
  lng: number
  hive_count: number
  start_date: string
  end_date: string | null
  notes: string | null
  status: "safe" | "warning" | "danger"   // computed from active sprays nearby
  current_risk: {
    nearest_spray_km: number | null
    nearest_spray_eta: string | null      // ISO timestamp
    active_alerts: number
  }
  created_at: string
  last_ledger_hash: string
}
```

---

## 3. Parcels (Parcele) — `fermier` only

Mock cadastru data. Pre-seeded per farmer.

### `GET /parcels`
Response 200: `{ items: Parcel[] }`.

### `GET /parcels/:id`
Response 200: `{ parcel: Parcel }`.

```ts
type Parcel = {
  id: string
  owner_id: string
  name: string                // "Parcela Apahida Nord"
  cadastral_number: string    // mock
  lat: number                 // centroid
  lng: number
  surface_ha: number
  default_crop: string | null
  county: string
  locality: string
}
```

---

## 4. Spray Reports (Rapoarte Stropire) — `fermier` writes, all roles read where applicable

### `POST /spray-reports`
**The heart of the product.** Triggers the full notification cascade.

Body:
```ts
{
  parcel_id: string
  surface_ha: number            // may differ from parcel default if partial
  crop: string                  // "rapiță", "porumb", ...
  substance: string             // exact label, e.g. "Confidor Energy"
  scheduled_at: string          // when spraying starts
  duration_hours: number        // estimated
  notes?: string
}
```
Side effects (all wrapped in a single ledger event chain):
1. Validates farmer owns the parcel.
2. Resolves substance → toxicity → risk radius.
3. Fetches current wind direction for the parcel coords (real weather API — see §9).
4. Finds all apiaries within risk radius (with downwind weighting — apiaries directly downwind get the alert even if slightly outside circle).
5. Creates `AlertDispatch` records for each apiary.
6. **Kicks off async cascade per dispatch:** push (immediate) + voice call (immediate) → SMS fallback (60s if not confirmed).
7. Generates PDF for the primărie of the farmer's locality.
8. Sends email with PDF attachment to that primărie's official registry inbox.
9. Writes ledger events: `spray.created`, `alert.dispatched` (×N), `pdf.generated`, `email.sent`.

Response 201:
```ts
{
  spray_report: SprayReport,
  ledger_hash: string,
  affected_apiaries: number,
  primarie_pdf_url: string,          // "/spray-reports/:id/primarie-pdf"
  initial_dispatches: AlertDispatchPublic[]   // each with initial states
}
```

### `GET /spray-reports`
Query: `?status=active|past&limit=&cursor=`
Response 200: `{ items: SprayReportSummary[], next_cursor: string | null }`

### `GET /spray-reports/:id`
Response 200: `{ spray_report: SprayReport, cascade: CascadeStatus }`. Inspector and owner only.

### `GET /spray-reports/:id/cascade-status`
**Polled every 2s by the farmer's UI** while overall_status !== "complete". Cheap endpoint — heavily cacheable per second.
Response 200: `CascadeStatus` (see §11).

### `GET /spray-reports/:id/primarie-pdf`
Response 200: `application/pdf`. The legal notification.

### `POST /spray-reports/:id/cancel`
Cancels future spraying. Triggers `spray.cancelled` ledger event + push to all originally alerted beekeepers. Response 200: `{ spray_report }`.

```ts
type SprayReport = {
  id: string
  farmer_id: string
  parcel_id: string
  parcel: { name: string, lat: number, lng: number }
  crop: string
  substance: string
  toxicity: "T-" | "T" | "T+"
  surface_ha: number
  scheduled_at: string
  duration_hours: number
  notes: string | null
  status: "scheduled" | "in_progress" | "completed" | "cancelled"
  affected_apiaries_count: number
  ledger_hash: string
  created_at: string
}
```

### `POST /spray-reports/anf-export` — `fermier` self / `inspector` for any farmer
Body: `{ farmer_id?: string, from: string, to: string }` (defaults: self, last 3 years).
Response 200: `application/pdf`. The 3-year audit registry. Ledger-backed; tamper-evident appendix.

---

## 5. Alerts (Alerte) — `apicultor` only

### `GET /alerts`
Query: `?status=active|all`
Response 200: `{ items: AlertView[] }`

### `GET /alerts/:id`
Response 200: `{ alert: AlertView, spray_report: SprayReport }`.

### `POST /alerts/:id/confirm`
In-app confirmation. Updates the underlying `AlertDispatch.in_app_confirmed_at` and `final_status` if not already set.
Body: `{ action: "move_hives" | "seal_in_place" }`
Response 200: `{ alert: AlertView, ledger_hash: string }`.

```ts
type AlertView = {
  alert_dispatch_id: string
  spray_report_id: string
  farmer_name_masked: string   // "M. Popescu"
  apiary_id: string
  apiary_name: string
  substance: string
  toxicity: "T-" | "T" | "T+"
  scheduled_at: string
  distance_km: number
  downwind: boolean
  spray_lat: number
  spray_lng: number
  wind_direction_deg: number
  channels: ChannelStates   // see §11
  final_status: FinalStatus
  in_app_action: "move_hives" | "seal_in_place" | null
  ledger_hash: string
  created_at: string
}
```

---

## 6. Damage Claims (Pagube) — `apicultor` writes, `inspector` reads — **optional for v1, ship if time**

### `POST /damage-claims`
Body: `{ apiary_id, related_spray_id?, description, hive_loss_count, photos: string[] (URLs after upload), gps_lat, gps_lng }`
Side effect: `damage.filed` ledger event, references `spray.created` hash if related.

### `GET /damage-claims` (own, or all if inspector)
### `GET /damage-claims/:id`

```ts
type DamageClaim = {
  id: string
  beekeeper_id: string
  apiary_id: string
  related_spray_id: string | null
  description: string
  hive_loss_count: number
  photos: string[]
  gps_lat: number
  gps_lng: number
  status: "filed" | "under_review" | "accepted" | "rejected"
  ledger_hash: string
  created_at: string
}
```

### `POST /uploads/sign` — get a signed URL or use API-side upload
Body: `{ filename, mime, byte_size }`
Response 200: `{ upload_url: string, public_url: string, fields?: object }`.
(Implementation: local disk under `/uploads` with a UUID filename for hackathon. Returns a public URL served by the API.)

---

## 7. Inspector — `inspector` only

### `GET /inspector/map-data`
Query: `?bbox=lat1,lng1,lat2,lng2`
Response 200:
```ts
{
  apiaries: { id, lat, lng, status, hive_count }[],
  active_sprays: { id, lat, lng, toxicity, scheduled_at, radius_m }[],
  damage_claims: { id, lat, lng, status }[]
}
```

### `GET /inspector/farmers`
Response 200: `{ items: FarmerSummary[] }`

### `GET /inspector/farmers/:id`
Response 200: `{ farmer: User, sprays_total: number, sprays_last_30d: number, damages_filed_against: number }`

---

## 8. Ledger (Registru distribuit)

### `GET /events?type=&actor=&limit=&cursor=`
Response 200: `{ items: LedgerEvent[], next_cursor }`.

### `GET /events/:hash`
Response 200: `{ event: LedgerEvent, chain: { prev_hash, next_hash | null } }`.

### `GET /events/verify`
Verifies the entire chain. Response 200: `{ valid: boolean, total_events: number, last_hash: string, checked_at: string }`.

```ts
type LedgerEvent = {
  id: string
  hash: string                 // sha256 of (prev_hash || canonical_json(payload) || created_at)
  prev_hash: string | null
  type: string                 // "spray.created" | "alert.dispatched" | "alert.confirmed" | "damage.filed" | "apiary.registered" | "spray.cancelled" | ...
  actor_id: string | null
  payload: object              // domain-specific
  created_at: string
}

type LedgerEventSummary = {
  hash: string
  type: string
  created_at: string
}
```

---

## 9. Reference / lookup data

### `GET /reference/substances`
Response 200: `{ items: { label: string, toxicity: "T-" | "T" | "T+" }[] }`
Pre-seeded list: Confidor Energy (T+), Mospilan 20 SG (T), Karate Zeon (T), Decis Mega (T-), etc.

### `GET /reference/weather?lat=&lng=`
Response 200: `{ wind_direction_deg: number, wind_speed_ms: number, temperature_c: number, fetched_at: string }`
Internal use mostly; exposed for the frontend to draw the wind cone on the map.

---

## 10. Web Push subscriptions

### `GET /push/vapid-public-key`
Response 200: `{ key: string }`.

### `POST /push/subscriptions`
Body: standard browser `PushSubscription.toJSON()` shape.
Response 201: `{ id: string }`.

### `DELETE /push/subscriptions/:id`
Response 204.

---

## 11. The Cascade — state machines & status shape

This is the part both sides must implement identically.

### Channel state machines

```
push:  pending → sent → delivered → opened
                                  → (no event = remains "delivered")

call:  queued → ringing → answered → confirmed   (user pressed 1)
                                   → no_input   (user pressed nothing within 10s)
                       → no_answer              (rang out / mailbox)
                       → busy
                       → failed
                       (after answered: also possible: hung_up)

sms:   skipped                                  (call confirmed in time, never sent)
       queued → sent → delivered → confirmed    (user replied "DA")
                                 → no_reply
                                 → failed
```

**Cascade orchestration rules:**
- `push` and `call` fire immediately, in parallel, on `POST /spray-reports`.
- If `call.final ∈ {confirmed}` within 60s of POST, SMS is set to `skipped`.
- If `call.final ∉ {confirmed}` after 60s **OR** call reaches a terminal non-confirmed state earlier (`no_answer`, `busy`, `failed`, `no_input`), SMS is queued immediately.
- For `T-` (low toxicity) substances, **skip the call entirely** — push + SMS only. Set `call.state = "skipped"` in the response.
- `in_app_confirmed_at` can be set independently if the beekeeper opens the app and taps confirm — this also sets `final_status = "confirmed_app"` if no other channel has won yet.

### `final_status`
`"confirmed_call" | "confirmed_sms" | "confirmed_app" | "unconfirmed" | "failed"`

First channel to reach a `confirmed` state wins. If no channel confirms after 30 minutes, `final_status = "unconfirmed"`. If all channels error, `final_status = "failed"`.

### `GET /spray-reports/:id/cascade-status` response

```ts
type CascadeStatus = {
  spray_report_id: string
  overall_status: "in_progress" | "complete"
  // "complete" when every dispatch has final_status set
  summary: {
    total: number
    confirmed: number   // any confirmed_*
    pending: number
    unconfirmed: number
    failed: number
  }
  dispatches: AlertDispatchPublic[]
  polled_at: string
}

type AlertDispatchPublic = {
  alert_dispatch_id: string
  beekeeper_initials: string        // "A. B."
  apiary_name: string
  distance_km: number
  downwind: boolean
  channels: ChannelStates
  final_status: FinalStatus | null
  ledger_hash: string
}

type ChannelStates = {
  push: { state: PushState, at: string | null }
  call: { state: CallState, at: string | null, attempts: number }
  sms:  { state: SmsState,  at: string | null }
}

type PushState = "pending" | "sent" | "delivered" | "opened"
type CallState = "skipped" | "queued" | "ringing" | "answered" | "confirmed" | "no_input" | "no_answer" | "busy" | "failed" | "hung_up"
type SmsState  = "skipped" | "queued" | "sent" | "delivered" | "confirmed" | "no_reply" | "failed"
type FinalStatus = "confirmed_call" | "confirmed_sms" | "confirmed_app" | "unconfirmed" | "failed"
```

---

## 12. Internal webhook endpoints (Twilio)

Not for the frontend. Documented here so the API team knows what to build and the Twilio console can be configured.

### `POST /webhooks/twilio/voice/status`
Twilio status callback. Updates `call.state` on the matching dispatch.

### `POST /webhooks/twilio/voice/gather`
Twilio's `<Gather>` action URL. Receives the DTMF input (1 = confirmed, 2 = open app). Returns TwiML for the next step.

### `POST /webhooks/twilio/sms/inbound`
Beekeeper SMS reply. If body contains "DA" (case-insensitive, trimmed), sets `sms.state = "confirmed"`.

### `POST /webhooks/twilio/sms/status`
SMS delivery status callbacks.

All four endpoints validate Twilio signature header `X-Twilio-Signature`.

---

## 13. Data model summary (for migration writing)

Tables to create (Postgres):
- `users` (id, cnp UNIQUE, full_name, email, phone, role, county, locality, password_hash, created_at)
- `auth_challenges` (id, user_id, method, code_hash, expires_at, verified_at)
- `apiaries` (id, owner_id, name, type, lat, lng, hive_count, start_date, end_date, notes, created_at)
- `parcels` (id, owner_id, name, cadastral_number, lat, lng, surface_ha, default_crop, county, locality)
- `spray_reports` (id, farmer_id, parcel_id, crop, substance, toxicity, surface_ha, scheduled_at, duration_hours, notes, status, created_at)
- `alert_dispatches` (id, spray_report_id, beekeeper_id, apiary_id, distance_m, downwind, push_state, push_at, call_state, call_at, call_attempts, sms_state, sms_at, in_app_confirmed_at, in_app_action, final_status, twilio_call_sid, twilio_sms_sid, created_at)
- `damage_claims` (id, beekeeper_id, apiary_id, related_spray_id, description, hive_loss_count, gps_lat, gps_lng, status, created_at)
- `damage_photos` (id, damage_claim_id, url)
- `push_subscriptions` (id, user_id, endpoint, p256dh, auth, created_at)
- `ledger_events` (id, hash UNIQUE, prev_hash, type, actor_id, payload JSONB, created_at) — INSERT-only via a transactional helper that locks the chain tip

Indexes: `apiaries(lat, lng)` (GiST or just btree on each for hackathon), `spray_reports(scheduled_at)`, `alert_dispatches(spray_report_id)`, `ledger_events(prev_hash)`.

---

## 14. Out of scope for v1

- Real estate cadastru integration (parcels are mock)
- Real ROeID (simulated only)
- Pastoral movement multi-locality routing (single primărie email)
- Inspector decision/action on damage claims (read-only review for v1)
- Multi-language UI (Romanian only)
- Real email infrastructure (SendGrid sandbox / Mailtrap is fine; primărie inbox is mocked but the email actually sends)