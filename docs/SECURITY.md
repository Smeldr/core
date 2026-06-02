# Security Policy

## Supported versions

Only the latest stable release receives security fixes.
Security-critical patches are released as patch versions (e.g. v1.22.1)
and tagged immediately.

| Version | Supported |
|---------|-----------|
| Latest stable (v1.22.x) | ✅ |
| Older versions | ✗ |

## Reporting a vulnerability

Email **xwink@proton.me** with the subject line `[smeldr] Security vulnerability`.

Please include:
- A clear description of the issue
- Steps to reproduce or a proof-of-concept (if available)
- Affected version(s)

Expected response time: **within 72 hours**.

We will acknowledge receipt, investigate, and coordinate a fix. Please do
not open a public GitHub issue for vulnerabilities until a fix is available.

## Threat model

### What Smeldr is responsible for

Smeldr enforces the following security properties automatically:

- **Content lifecycle enforcement** — Draft and Archived items return 404 to
  all unauthenticated requests; only Published items are publicly visible.
- **Role-based access control (RBAC)** — every HTTP and MCP operation is gated
  by the `Guest → Author → Editor → Admin` hierarchy. Roles are enforced per
  module, per operation.
- **Token validation** — Bearer HMAC tokens are verified on every request.
  When `TokenStore` is configured, revocation is checked on every call.
- **SSRF-safe outbound** — webhook endpoints must be HTTPS and must not resolve
  to private/loopback addresses. Validation runs at registration and at delivery.
- **Webhook HMAC signing** — all outbound webhook payloads are signed with
  HMAC-SHA256. The signature is transmitted in the `X-Forge-Signature` header.
- **AES-256-GCM credential encryption** — smeldr.dev/social stores OAuth app
  credentials encrypted at rest using AES-256-GCM.
- **Path traversal prevention** — smeldr.dev/media uses `os.Root` (Go 1.24+) to
  confine all file operations to the configured upload directory.
- **MIME whitelist** — smeldr.dev/media enforces a strict MIME type allowlist for
  file uploads; magic-byte detection prevents type confusion.

### What the developer is responsible for

Smeldr cannot protect against:

- **Insecure handler code** — your own HTTP handlers, template logic, and
  business rules are outside Smeldr's security boundary.
- **Template injection** — Smeldr uses Go's `html/template` for HTML rendering,
  which auto-escapes template variables. However, `forge_html` fields emit
  verbatim HTML — the developer is responsible for sanitising that content
  before storage.
- **Deployment security** — HTTPS termination, reverse proxy configuration,
  and infrastructure hardening are the operator's responsibility.
- **Secret management** — `FORGE_SECRET` must be kept out of source control
  (see Production security checklist below).

## Auth model

- **Bearer token** — clients authenticate via `Authorization: Bearer <token>`.
  Tokens are HMAC-SHA256 signed. When `TokenStore` is configured, tokens are
  also stored and can be revoked.
- **Role hierarchy** — `Guest < Author < Editor < Admin`. Each operation
  declares a minimum required role.
- **`ensureBootstrap`** — on first start, if no admin token exists, Smeldr
  automatically creates one and prints it to stdout. Copy it immediately —
  it cannot be retrieved again.
- **Token revocation** — `TokenStore.Revoke(id)` is effective immediately.
  Revocation is permanent; a revoked token cannot be restored.
- **ErrLastAdmin guard** — Smeldr refuses to revoke the last active admin token.
  Create a replacement before revoking.

## Webhook signing

Outbound webhooks are signed using HMAC-SHA256.

**Signed string format:**
```
<unix-timestamp-seconds>.<raw-body>
```

**Header:**
```
X-Forge-Signature: sha256=<hex-encoded-HMAC>
```

**Verification (Go example):**

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
expected := hex.EncodeToString(mac.Sum(nil))
if !hmac.Equal([]byte(received), []byte(expected)) {
    // reject
}
```

Signing secrets are generated at webhook registration and returned once.
They are stored AES-256-GCM encrypted in the webhook store.

## OAuth credential storage (smeldr.dev/social)

smeldr.dev/social stores OAuth 2.0 application credentials (client ID, client
secret, access tokens) AES-256-GCM encrypted in the database. The encryption
key is derived from `FORGE_SECRET`. Credentials are never stored in plaintext.

## Media upload safety (smeldr.dev/media)

- **MIME whitelist** — only `image/jpeg`, `image/png`, `image/webp`,
  `image/gif`, and `image/avif` are accepted. MIME type is verified from
  magic bytes, not the `Content-Type` header.
- **Path traversal** — all file I/O is performed via `os.Root` (Go 1.24+),
  which confines operations to the configured upload directory. Filenames
  are sanitised and prefixed with a random hex string.
- **Token-scoped uploads** — `Authorization: UploadToken <token>` restricts
  uploads to images only (stricter than the standard Bearer-token path).

## Production security checklist

Before deploying Smeldr to production:

- [ ] **HTTPS** — terminate TLS at the load balancer or reverse proxy;
      pass `smeldr.TrustedProxy()` to `smeldr.RateLimit(...)` if Smeldr is behind
      a reverse proxy so rate limiting uses the real client IP.
- [ ] **`FORGE_SECRET`** — set via environment variable, not in a config
      file committed to source control. Use a random 32-byte value minimum.
- [ ] **Token scoping** — issue Author-role tokens to agents; reserve
      Admin tokens for humans and automation that explicitly needs admin access.
- [ ] **Secret rotation** — rotate `FORGE_SECRET` by issuing new tokens
      before invalidating the old secret. Rotate OAuth credentials on personnel
      changes.
- [ ] **`os.Root` available** — if using smeldr.dev/media, ensure you are running
      Go 1.24 or later (required for path traversal protection).
- [ ] **Security headers** — call `smeldr.SecurityHeaders()` in your middleware
      chain to enable CSP, HSTS, X-Frame-Options, and Referrer-Policy:
      `app.Use(smeldr.RequestLogger(), smeldr.Recoverer(), smeldr.SecurityHeaders())`
