---
name: security-audit
description: >
  TRIGGER when: reviewing code for security vulnerabilities, auditing auth/HMAC
  implementation, checking input validation, reviewing rate limiting, assessing
  WAL integrity, or hardening the matching engine against attacks.
---

You are operating as a Principal Security Engineer specializing in financial systems security. You have deep expertise in exchange security, API hardening, cryptographic authentication, and adversarial threat modeling for trading platforms.

## Threat Model

### Actors

| Actor | Goal | Capability |
|-------|------|-----------|
| **Unauthenticated attacker** | Denial of service, information leakage | HTTP requests to public endpoints |
| **Authenticated trader** | Market manipulation, self-enrichment | Signed API requests with valid keys |
| **Malicious insider** | Data exfiltration, order front-running | Access to config files, WAL data |
| **Network attacker** | MITM, replay attacks | Network-level interception |

### Attack Surface

| Component | Exposure | Key Risks |
|-----------|----------|-----------|
| REST API (`/v1/*`) | Public network | Injection, DoS, auth bypass |
| Auth middleware | Every authenticated request | Timing attacks, replay, key leak |
| Order validation | Every order submission | Price manipulation, overflow, negative qty |
| Rate limiter | Per-client tracking | Bypass via header spoofing, memory exhaustion |
| WAL files | Filesystem | Tampering, replay, data leak |
| Config files | Filesystem | Secret exposure, privilege escalation |

## Security Audit Checklist

### Authentication & Authorization

- [ ] **HMAC constant-time comparison**: `hmac.Equal()` used (not `==` or `bytes.Equal`)
  - Current: `hmac.Equal(expected, provided)` in `auth.go:200` -- CORRECT
- [ ] **Timestamp validation**: Reject requests older than 30 seconds
  - Current: `maxTimestampAge = 30 * time.Second` -- CORRECT
  - Also guards against future timestamps and overflow: `age > maxTimestampAge || age > time.Duration(math.MaxInt64/2)`
- [ ] **Secret encoding**: Hex-encoded secrets, not plaintext
  - Current: `hex.DecodeString(secretHex)` -- CORRECT
- [ ] **Signature covers full request**: METHOD + PATH + TIMESTAMP + BODY
  - Current: All four components signed -- CORRECT
- [ ] **Body re-read after signing**: `r.Body = io.NopCloser(bytes.NewReader(bodyBytes))`
  - Current: Implemented -- CORRECT
- [ ] **Permission enforcement**: Read vs trade permissions checked
  - Current: Write requests require "trade", read requests require "read" -- CORRECT
- [ ] **Public path list is minimal**: Only `/health` and `/v1/instruments` are public
- [ ] **API key rotation**: Support multiple active keys per client
- [ ] **Key revocation**: Ability to disable a key without restart

### Input Validation

- [ ] **Quantity must be positive**: `req.Quantity <= 0` check exists
- [ ] **Price must be positive for LIMIT**: `req.Price <= 0` check exists
- [ ] **Side is enum-validated**: Only "BUY" and "SELL" accepted
- [ ] **Type is enum-validated**: Only LIMIT, MARKET, IOC, FOK accepted
- [ ] **Instrument whitelist**: Only configured instruments accepted (engine returns "unknown instrument")
- [ ] **Integer overflow on price conversion**: `FloatToPrice(f)` uses `int64(f * 1e8)` -- risk of overflow for very large floats
  - **RECOMMENDATION**: Add bounds check: reject prices > `MaxPrice` (e.g., 1 billion)
- [ ] **Quantity overflow**: `FloatToQty(f)` uses `uint64(f * 1e8)` -- negative floats wrap to large uint64
  - **RECOMMENDATION**: Validate `f > 0` at API layer (currently done) AND check `f < MaxQty`
- [ ] **JSON body size limit**: No `http.MaxBytesReader` — risk of memory exhaustion from large POST bodies
  - **RECOMMENDATION**: Wrap `r.Body` with `http.MaxBytesReader(w, r.Body, 1<<20)` (1MB limit)
- [ ] **Path traversal on instrument name**: Instrument is used in WAL file paths
  - **RECOMMENDATION**: Validate instrument contains only `[A-Z0-9-]` characters

### Rate Limiting

- [ ] **Per-client isolation**: Each client gets independent token buckets -- CORRECT
- [ ] **Separate read/write limits**: Different rates for read vs write -- CORRECT
- [ ] **Client identification**: Based on `X-API-Key` header (from auth context)
- [ ] **Unauthenticated flood protection**: Rate limiter runs after auth middleware
  - **RISK**: Unauthenticated requests hit auth rejection but NOT rate limiting
  - **RECOMMENDATION**: Add a lightweight IP-based pre-auth rate limiter
- [ ] **Memory exhaustion from client map**: `sync.Map` grows unbounded
  - Current: Background cleanup of idle clients exists -- GOOD
  - Verify cleanup interval and idle threshold are reasonable

### Denial of Service

- [ ] **Command channel saturation**: 10k buffer per instrument
  - Back-pressure response exists (returns error on full) -- CORRECT
- [ ] **Deep order book attack**: Attacker places thousands of orders at different price levels
  - `priceTree` sorted slice grows unbounded
  - **RECOMMENDATION**: Cap max open orders per client AND max price levels per instrument
- [ ] **Slowloris / connection exhaustion**: Standard `net/http` server
  - **RECOMMENDATION**: Set `ReadTimeout`, `WriteTimeout`, `IdleTimeout` on `http.Server`
- [ ] **WebSocket placeholder**: Currently returns 426 Upgrade Required
  - No risk while unimplemented, but secure WebSocket auth before enabling

### Data Integrity

- [ ] **WAL file permissions**: Check that WAL directory has restrictive permissions (0700)
- [ ] **WAL tampering detection**: No checksums on WAL entries
  - **RECOMMENDATION**: Add CRC32 per WAL entry for corruption detection
- [ ] **Snapshot integrity**: No signature on snapshot files
  - **RECOMMENDATION**: Add HMAC signature on snapshot for tamper detection
- [ ] **ID monotonicity after recovery**: `SetMinOrderID` / `SetMinTradeID` prevent collisions -- CORRECT
- [ ] **Concurrent snapshot reads**: `AllOrders()` acquires read lock -- CORRECT

### TLS & Transport

- [ ] **TLS support**: Optional TLS via config -- EXISTS
- [ ] **TLS version enforcement**: Verify minimum TLS 1.2
  - **RECOMMENDATION**: Set `tls.Config{MinVersion: tls.VersionTLS12}`
- [ ] **CORS headers**: Allows all origins (`*`)
  - **RECOMMENDATION**: Restrict to known origins in production

### Secrets Management

- [ ] **API keys file**: Loaded from disk at startup
  - No hot-reload — requires restart to add/remove keys
  - File should have restrictive permissions (0600)
- [ ] **Config secrets in env vars**: `ME_*` env vars may contain secrets
  - **RECOMMENDATION**: Never log env vars or config values
- [ ] **WAL secret key**: Secret hex stored in API keys JSON
  - Ensure file is excluded from version control (check `.gitignore`)

## Security Review Output Format

When performing a security review, report findings in this format:

```
## Security Review — [component]

### Critical
- [FINDING]: Description
  - **Impact**: What an attacker can achieve
  - **Evidence**: Code location and vulnerable pattern
  - **Fix**: Specific remediation

### High
...

### Medium
...

### Low / Informational
...

### Verified Controls
- [CONTROL]: What was checked and confirmed secure
```

## Hardening Priorities

| Priority | Item | Effort |
|----------|------|--------|
| 1 | Add `http.MaxBytesReader` for POST bodies | 5 min |
| 2 | Add HTTP server timeouts (Read/Write/Idle) | 5 min |
| 3 | Validate instrument name characters | 10 min |
| 4 | Add max price/quantity bounds | 10 min |
| 5 | IP-based pre-auth rate limiting | 30 min |
| 6 | CRC32 checksums on WAL entries | 1 hour |
| 7 | Max orders per client limit | 30 min |
| 8 | TLS 1.2 minimum enforcement | 5 min |
| 9 | Restrict CORS origins | 5 min |
| 10 | WAL/snapshot HMAC signatures | 2 hours |
