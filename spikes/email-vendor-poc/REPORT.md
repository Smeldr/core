# Transactional email vendor POC — Lettermint vs. Sweego

Status: **Closed.** Flows 1-2 (outbound) complete for both vendors — real sends,
delivery confirmed to inbox, links unmodified. Flows 3-5 replaced by a click-tracking
webhook test (scope pivot, see below); Lettermint's webhook richness confirmed
unprompted, Sweego's click tracking did not register within the test window. See
"Recommendation" at the end for Peter's requested personal read on top of the evidence.

## Scope pivot, 2026-07-24: flows 3-5 (full inbound routing) replaced with a "clicked" event test

Peter's own question, worth recording verbatim in reasoning: *why does Smeldr need to
react to inbound email at all?* Checked against the spec itself — flows 3-5 exist
because the spec's own text admits inbound "isn't in any current Smeldr scope beyond
this spike"; it's a forward-looking capability check (webhook payload richness), not a
requirement of the two actual flows Smeldr ships (magic-link login, org invite), both of
which are outbound-only with no reply/receive step.

Given that, and given the real setup cost surfaced while investigating (Sweego: paid
tier + dedicated subdomain + MX record; Lettermint: dashboard route config; neither
serves a real current Smeldr feature), Peter proposed a lighter, more directly relevant
substitute: **test a "clicked" event webhook instead** — does each vendor tell Smeldr
when a recipient actually used the magic-link, using infrastructure already built and
verified (the local webhook receiver + ngrok tunnel, already reachable end-to-end).
This is arguably more useful than full inbound-email receiving would have been: a real
future use for Smeldr (knowing whether a magic-link was actually clicked) rather than a
speculative one.

- **Sweego**: confirmed via the dashboard's "Add new webhook" panel — full EMAIL trigger
  list is `sent`, `delivered`, `soft bounce`, `hard bounce`, `spam complaints`,
  `list-unsubscribe`, `click`, `click unsubscribe`, `open` (parallel SMS list also
  present). `click` is available, no paid-tier gate encountered (unlike inbound).
- **Lettermint**: equivalent webhook/tracking-event page not yet located — their docs
  site's sidebar navigation didn't yield it via automated browsing (same
  client-rendered-doc friction pattern noted for Sweego). Asked Peter to check the
  Lettermint dashboard UI directly instead, the same fast approach that resolved the
  Sweego credential mix-up earlier.

Flows 3-5 (full inbound routing) are not being pursued further — this is a scope
decision, not an incomplete task; see reasoning above.

Spec: `smeldr/architect/analysis/transactional-email-poc-2026-07-19.md`.
Deliverable: evidence for Peter's decision, not a recommendation (per spec's explicit
scope).

---

## Sweego — RESOLVED: `SWEEGO_API_KEY`/`SWEEGO_API_SECRET` were swapped in NEXT.md

NEXT.md's `SWEEGO_API_KEY` value (`0588c0ad-...`) was actually the dashboard's **Key ID**
(an identifier, not a usable credential) — confirmed from a screenshot of the Sweego
Credentials panel, which shows "Key ID" and the real, masked "Api-Key" as two distinct
fields. NEXT.md's `SWEEGO_API_SECRET` value (`b457d839-...`) turned out to be the actual
**Api-Key** secret, confirmed directly by Peter copying it from the dashboard's masked
field. `.env` corrected (comment added explaining the swap); both real sends succeeded
immediately after:

```
$ go run . -vendor=sweego -flow=1 -from=noreply@transactional.smeldr.io -to=peter@smeldr.dev
sweego: sent, transaction_id=8b966381-fb15-4557-b8e6-3e2a1cfba7f4 credit_left=
raw response: {"channel":"email","provider":"sweego","swg_uids":{"peter@smeldr.dev":"02-b6294955-6dcd-4d6f-b34f-3a1d27a6d1ae"},...}

$ go run . -vendor=sweego -flow=2 -from=noreply@transactional.smeldr.io -to=peter@smeldr.dev
sweego: sent, transaction_id=fc25dc3d-9736-4df0-9968-24a98abdb151 credit_left=
```

**Delivery confirmed by Peter directly** (flow 1, `peter@smeldr.dev`, 2026-07-24 12:19):
- **Landed in inbox**, not spam.
- Sender, subject, and body rendered exactly as sent — `noreply@transactional.smeldr.io`,
  "Your Smeldr sign-in link", link text unchanged.
- **Link was NOT rewritten by Sweego** — `https://smeldr.example/auth/magic?token=poc-test-token-abc123`
  reached the inbox byte-for-byte identical to what this spike sent, no tracking-redirect
  wrapping. Directly satisfies the spec's flow-1 requirement ("verify the link received
  matches what was sent — no vendor-side rewriting breaking the token").
- Latency: sent and received within the same minute (not precisely timed, but clearly
  fast — no multi-minute delay observed).

**Scope correction (Peter, 2026-07-24):** the spec's "Microsoft 365 test mailbox and a
Gmail test mailbox specifically" requirement was the architect's own rephrasing of
Peter's original ask — his actual private mailbox, `peter@smeldr.dev`. Not two separate
M365/Gmail test accounts. **This delivery result above already satisfies flow 1's
delivery-result measurement in full** — no further mailbox-specific testing needed for
that metric.

Both flow 1 (magic-link) and flow 2 (org-invite) sent successfully to `peter@smeldr.dev`.

## Lettermint — RESOLVED (unblocked with a fresh token, one more real code bug found)

Not the same class of issue as Sweego. Peter's Lettermint support ticket
(`T-8ECC3395`, shared directly) shows Lettermint gates *any* sending behind a manual
compliance/verification step — their reviewer (Bjorn) rejected the initial verification
because "the provided website isn't active," which Peter disputes (smeldr.dev is live).
**This means the 401 was never a credential-value problem for Lettermint** — the account
itself isn't cleared to send yet, so no token value would have worked. This is a real,
reportable data point on its own: Lettermint requires human-reviewed compliance
verification before first send; Sweego's API-key self-serve flow had no equivalent gate
encountered in this spike.

Checked what an external verifier might see, to help narrow down Lettermint's "not
active" claim:
- `https://smeldr.dev` — 200 OK, 0.13-0.21s response time, correct security headers
  (HSTS, X-Frame-Options, etc.), `robots.txt` allows all crawlers (`Disallow:` empty),
  same result with a custom bot-like User-Agent. Nothing here looks like it would read as
  "inactive" to an automated checker.
- `https://www.smeldr.dev` — **TLS handshake fails** (`SEC_E_INTERNAL_ERROR`, Windows
  schannel). DNS resolves correctly to the same IP as the bare domain
  (`178.104.61.248`), so this isn't a DNS problem — looks like either a missing
  SNI/certificate binding for the `www` host on the server (Caddy, per the bare domain's
  `Via` header) or a local Windows TLS-stack artifact specific to this machine; not
  fully distinguished between the two from here. Worth checking from a different
  network/OS to confirm whether it's a real server-side gap. The submitted verification
  used the bare domain (`smeldr.dev`), not `www`, so this may not even be related to
  Lettermint's specific rejection — flagging as a finding, not a confirmed cause.

Not something this spike can resolve further — it's Peter's own verification
conversation with Lettermint's support, not a code or credential fix. Sends against
Lettermint stay blocked until that ticket resolves.

**Tried the documented unverified-account workaround, still 401.** Lettermint's own
site states unverified accounts can still send, restricted to the account's own signup
address. Re-ran flow 1 targeting `peter@smeldr.dev` — same `401 "Provided API token is
invalid"`, unchanged. A restriction on the recipient address would more plausibly surface
as a 403 or a specific validation error, not a 401 on the token itself — this account
looks like it's in a stricter held-for-review state than the generic "unverified"
default the workaround describes, consistent with an active human compliance dispute
(the support ticket) rather than a routine not-yet-verified account. Confirms this stays
blocked on Lettermint support resolving the ticket; not a workaround this spike can use.

**Unblocked, 2026-07-24**: Peter issued a fresh token from the same Lettermint project.
The old token consistently returned `401 "Provided API token is invalid"` (compliance
hold or a stale/misconfigured token — never fully distinguished, and no longer
diagnosable now that the hold is moot for testing purposes); the new token authenticated
immediately, surfacing a **different, real bug in this spike's own code** on the very
next attempt:

```
$ go run . -vendor=lettermint -flow=1 ...
422: {"message":"The to field must be an array.","errors":{"to":["The to field must be an array."]}}
```

NEXT.md's documented shape had `to` as a plain string; the real API requires an array.
Not discoverable from Lettermint's own docs (their Go-guide page documents only the
official SDK's fluent builder, which hides this detail behind `.To(...)`), only found by
reading the real API's own validation error. Fixed in `lettermint.go` (`To []string`).
Both flows then succeeded:

```
$ go run . -vendor=lettermint -flow=1 -from=noreply@transactional.smeldr.io -to=peter@smeldr.dev
lettermint: sent, message_id=b5ffe740-52bb-4513-8c56-359c30995387 status=pending

$ go run . -vendor=lettermint -flow=2 -from=noreply@transactional.smeldr.io -to=peter@smeldr.dev
lettermint: sent, message_id=c082c57c-31f3-464a-888c-e24188704602 status=pending
```

Both responses report `status: "pending"` (asynchronous — unlike Sweego's `/send`, which
returned a `swg_uids` map suggesting a more immediate per-recipient result). **Delivery
confirmed by Peter directly**: both flow 1 and flow 2 landed in inbox (not spam), sender/
subject/body/link preserved exactly as sent — same clean result as Sweego, despite the
async `status: "pending"` response suggesting there might be a gap before actual
delivery. No vendor-side link rewriting observed for Lettermint either.

Note the compliance verification ticket (`T-8ECC3395`) itself is still unresolved as far
as this spike knows — a new token from the same project sending successfully doesn't
necessarily mean the underlying compliance hold was lifted; may be worth Peter confirming
with Lettermint support whether the account is now actually verified, or whether this is
a loophole that could close later (e.g. if the new token itself later gets held the same
way).

**www.smeldr.dev TLS finding escalated separately** — Peter is routing the `www` TLS
handshake failure to the architect directly as its own fix, unrelated to this spike's
scope (production site infrastructure, not an email vendor comparison). Not actioned
further from this branch.

**Paused (2026-07-24):** waiting on Peter/Lettermint support before further Lettermint
work. Sweego's outbound flows are done; nothing else to do on this spike until either
side unblocks.

---

## Investigation trail (kept for the "documentation/API ergonomics" writeup)

**Round 1** (initial implementation): Lettermint via the REST shape NEXT.md specified,
Sweego via SMTP (guessed auth pattern, since Sweego's docs site would not render for
automated fetching). Both failed at the auth layer:
- Lettermint: `401 {"message":"Provided API token is invalid."}`
- Sweego SMTP: `535 "Authentication failed"` (TLS/connection fine, AUTH itself rejected)

**Round 2** (after Peter confirmed the Lettermint token and pointed at Sweego's real
`/send` API docs): used a real browser (WebFetch could not render `learn.sweego.io` —
JS-rendered SPA, returned only the nav shell on 5 separate attempts across different
doc pages; a browser tool that actually executes JS was needed instead) to read
Sweego's full API reference directly. Found:
- Auth is a single `Api-Key: <key>` header on `send/*` routes — not SMTP AUTH, not
  Bearer. SMTP access is a **separate** login/password pair generated independently in
  the dashboard, unrelated to `SWEEGO_API_KEY`/`SWEEGO_API_SECRET` — this explains the
  535 (wrong credential type entirely, not just a wrong pairing).
- Exact working request shape, including `"provider": "sweego"` (an undocumented-until-
  read required field with a fixed default value).
- Sweego's own inbound-email webhook payload example (full JSON, attachment metadata
  shape) — useful directly for flows 3-5's "webhook payload richness" comparison,
  without needing a real inbound test to discover it.
- **Inbound email is a paying-tier-only feature** (`learn.sweego.io/docs/inbound/setup_inbound`:
  "Note: Inbound feature is reserved for paying users") and requires an MX DNS record on
  a dedicated subdomain, verified through the dashboard — a real infrastructure change,
  not just a webhook-URL registration. Not yet confirmed whether Peter's Sweego account
  is on a tier that includes this.

Rewrote `sweego.go` to use the confirmed REST shape. Re-ran both flows — **still 401 for
both vendors**, unchanged from round 1. To rule out any bug in this spike's own HTTP
client code, re-tested both directly with `curl`, bypassing Go entirely:

```
$ curl -X POST https://api.lettermint.co/v1/send -H "x-lettermint-token: <token>" ...
HTTP 401

$ curl -X POST https://api.sweego.io/send -H "Api-Key: <key>" ...
HTTP 401
```

**Both confirmed independent of any code in this spike** — request shape was correct for
both vendors, the credential *values* were the actual problem. For Sweego, root cause
found and fixed (see "Sweego — RESOLVED" above): NEXT.md's `SWEEGO_API_KEY`/
`SWEEGO_API_SECRET` labels were swapped against the dashboard's own field names. Lines
of Go code needed didn't change — this was a values problem, not a code problem.
Lettermint remains open; see "Lettermint — still blocked" above.

## What's built and verified so far

- `spikes/email-vendor-poc/` — standalone Go module, zero dependencies (stdlib
  `net/http`/`encoding/json` only — both vendors are hit via REST now, `net/smtp` no
  longer needed after Sweego's real auth scheme was confirmed), `go build ./...`,
  `go vet ./...`, and `gofmt -l .` all clean.
- All 5 flows have dispatch code in `main.go`. **Flows 1-2 (outbound) confirmed working
  end-to-end for both vendors** — real sends, real message/transaction IDs, one real
  send delivery-confirmed to inbox (Sweego). Flows 3-5 (inbound) have a working local
  webhook receiver (`webhookserver.go`, `go run . -webhook`) that persists any received
  payload to `received/{vendor}/{timestamp}.json` — deliberately schema-agnostic rather
  than pre-guessing the payload shape from unreachable docs (see below); not yet exposed
  via a tunnel or registered in either vendor's dashboard.

## Documentation quality — early data point (part of the spec's own measurement list)

- **Lettermint**: docs fetched cleanly via plain automated fetch and are genuinely well
  written — but document only their official SDK (`github.com/lettermint/lettermint-go`),
  not the raw REST shape. The raw shape used here came from NEXT.md directly, not from
  Lettermint's own docs.
- **Sweego**: `learn.sweego.io` is entirely client-rendered (Docusaurus SPA) — plain
  automated fetching (WebFetch) returned only the navigation shell on every attempt,
  across 5+ different doc pages. A real browser tool (one that executes JS) was required
  to read anything past the homepage. Once actually readable, the content itself is
  thorough and precise — full request/response schemas, a real inbound webhook payload
  example, exact curl examples that worked first try. **The gap is accessibility, not
  quality**: "how fast a real question gets answered by reading docs alone" depends
  entirely on whether the reader's tooling can render a JS SPA. For a human with an
  ordinary browser this is probably a non-issue; for any automated/AI-agent consumer of
  Sweego's docs (a real scenario Smeldr itself cares about, being AI-native) it is a
  concrete, measurable weakness Lettermint's plain-HTML docs don't share.
- **Sweego, separately**: inbound email is gated to a paying tier
  (`docs/inbound/setup_inbound`) and requires an MX DNS record on a dedicated subdomain,
  verified through the dashboard — not discoverable from the `/send` API reference alone,
  only found by reading the dedicated inbound setup page.

## Lines of Go code so far (rough, per the spec's own "rough, not precise" framing)

| File | Lines | Vendor |
|------|------:|--------|
| `lettermint.go` | 68 | Lettermint |
| `sweego.go` | 88 | Sweego |
| `webhookserver.go` | 70 | shared (both, inbound receiver) |
| `main.go` | 141 | shared (dispatch + both) |

Sweego's dedicated code ended up larger than Lettermint's — most of the difference is
Sweego's richer request shape (`channel`, `provider`, structured `from`/`recipients`
address objects vs. Lettermint's flat strings) and the response struct's `swg_uids` map.
Both vendors now have real, confirmed-working outbound code at these sizes.

## API ergonomics — both vendors now confirmed working

- Sweego's `/send` request needed one field neither vendor's task description mentioned
  up front: `provider: "sweego"`, marked "Mandatory" with no listed valid-values set
  beyond the one example. Discovered only by reading the full API reference, not
  guessable from the endpoint name or Sweego's own field descriptions.
- Sweego's credential model has three distinct secrets living in one dashboard (REST
  Api-Key, a separate SMTP login/password pair, and a Key ID that looks like a credential
  but isn't) — more surface area for exactly the kind of mix-up this spike hit than
  Lettermint's single project token.
- Lettermint's `to` field is an array, not a plain string — the one real shape mismatch
  found, via the API's own validation error rather than any docs (neither NEXT.md's
  summary nor Lettermint's own Go-guide, which documents only the SDK, mentioned this).
- Both vendors needed exactly one real code fix each before their first successful send
  (Sweego: credential value; Lettermint: request shape) — the "clean first try" claim
  neither vendor's marketing implies held for neither, in different ways.
- Response shape differs meaningfully: Sweego returns a `swg_uids` map keyed by
  recipient (implies per-recipient tracking built into the send response itself);
  Lettermint returns one `message_id` + `status: "pending"` (async, single
  identifier, status presumably updates via webhook or a separate lookup — not
  explored yet in this spike).

## Click-tracking / webhook-richness result (closing this line of investigation)

**Lettermint**: its webhook fired unprompted, on the original flow-1 send, before this
line of investigation even started — three real events captured (`message.created`,
`message.sent`, `message.delivered`), each a rich, structured payload including the
real SMTP-level response (`"Ok: queued as C74191668C589"`, status code 250). No extra
setup needed beyond the webhook URL Peter had already registered. Confirmed genuine
(not a manual test artifact) by the `message_id` matching a real send and containing
data no manual browser visit could produce.

**Sweego**: click-through and open tracking were correctly configured (CNAME
`t.transactional.smeldr.io` → `t.sweego.co`, verified; both toggles on), and the link in
a resent email was correctly rewritten to a tracking-redirect URL. Peter clicked it for
real. Sweego's own "Engagement performance" dashboard reported **0 opens, 0 clicks**
immediately afterward — but this turned out to be a **dashboard/aggregate-stats lag, not
a tracking failure**. Sweego's own per-message "Logs" view later showed the click
recorded (status "Human click", correctly distinguishing a real user from a bot/proxy
pre-fetch), and the webhook itself arrived — delayed by roughly 15-20 minutes after the
actual click, not real-time:

```json
{
  "event_type": "email_clicked",
  "transaction_id": "cee5bb20-fb32-4379-8f2e-393b8f40b3a9",
  "recipient": "peter@smeldr.dev",
  "click": {
    "ip_address": "77.33.65.36",
    "url": "https://smeldr.example/auth/magic?token=poc-test-token-abc123",
    "proxy": false,
    "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) ... Chrome/150.0.0.0 ..."
  },
  "headers": { "x-swg-uid": "...", "x-client-id": "...", "x-campaign-id": "default", "...": "..." }
}
```

Genuinely rich: real client IP, user-agent, bot/proxy classification, the original
destination URL correctly recovered from the tracking redirect, plus a set of `x-swg-*`
tracing headers. The earlier "0 clicks, unresolved" read in this report was wrong —
correcting it here rather than leaving a stale conclusion standing. The real, reportable
finding is the **latency**: Sweego's click event took on the order of 15-20 minutes to
appear anywhere (dashboard aggregate, per-message log, and webhook all lagged together),
against Lettermint's message-lifecycle webhooks, which arrived within seconds of the
send. Both vendors deliver rich payloads; Sweego's click-tracking pipeline is
meaningfully slower than Lettermint's basic delivery webhooks — not a defect, but a real
operational-latency difference worth having in the comparison.

**Net result**: both vendors' webhook richness claims held up under real inspection.
Lettermint's arrived instantly with zero extra setup; Sweego's needed real
configuration (CNAME, toggles) and arrived with a real, double-digit-minute delay —
a genuine trade worth weighing, not a reliability failure on either side.

## Closing status

Flows 1-2 (outbound, both vendors) are complete: real sends, delivery confirmed to
inbox, links unmodified. Flows 3-5 (full inbound routing) were replaced by the
click-tracking test above per the scope pivot (inbound doesn't serve any current Smeldr
feature — see that section). The remaining spec metrics not covered by real testing in
this spike (rate limits, support response time, DKIM/SPF/DMARC setup effort in detail,
pricing at exact pilot volume) are left for Peter's own review of each vendor's pricing
page and terms — this spike answered the questions that needed real code and real sends
to answer; the rest is desk research, not a build.

---

## Recommendation (Peter asked directly in chat — the evidence above remains the primary deliverable, this is a personal read, not a decision)

Leaning **Lettermint**, with one real caveat.

**In Lettermint's favor:**
- Simpler credential model — one token, not three different secrets living in one
  dashboard the way Sweego's Api-Key/Key-ID/SMTP-login setup does. That confusion
  directly cost real debugging time in this spike.
- Docs are plain HTML, readable by ordinary automated tooling. Sweego's are a
  client-rendered SPA that no automated fetch could read (five separate failed
  attempts across different pages; only a real browser-rendering tool worked). For an
  AI-native product, a vendor whose own docs are unreadable to AI tooling is a real,
  structural weakness — any future agent-assisted debugging or re-implementation hits
  the same wall this spike did.
- Webhook payload richness held up unprompted — structured lifecycle events with real
  SMTP-level delivery confirmation, no extra setup.

**The real caveat:** the compliance-verification dispute with Lettermint's support
(`T-8ECC3395`) was never actually resolved — it was worked around with a freshly issued
token, not fixed. Whether that hold could reactivate, or whether a new token from the
same account carries the same restriction later, is genuinely unknown. This is the one
concrete operational risk weighing against Lettermint.

Sweego had no equivalent manual gate — self-serve API key, working immediately once the
credential-label mix-up was resolved. Its click-tracking does work with a genuinely rich
payload (real IP, user-agent, bot/proxy classification, correctly recovered destination
URL) — the credential model and inaccessible docs are the real, standing friction
points, not click-tracking reliability. The one substantiated knock against it here is
speed, not correctness: Sweego's click/engagement pipeline (dashboard, logs, and
webhook alike) ran ~15-20 minutes behind the actual click, against Lettermint's
message-lifecycle webhooks arriving within seconds. Worth weighing if anything in
Smeldr's own use of this data were latency-sensitive — magic-link/org-invite delivery
confirmation itself is not, so this is a minor factor, not a disqualifying one.
