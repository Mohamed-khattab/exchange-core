# Security Checklists Reference

## Pre-Launch Security Checklist

### Authentication
- [ ] All non-public endpoints require valid API key
- [ ] HMAC signature required for all write operations
- [ ] Timestamp validation prevents replay attacks (30s window)
- [ ] Constant-time signature comparison (`hmac.Equal`)
- [ ] Failed auth attempts are logged with client IP
- [ ] API keys are rotatable without downtime
- [ ] Revoked keys are rejected immediately

### Input Validation
- [ ] All numeric inputs have min/max bounds
- [ ] String inputs are length-limited
- [ ] Enum values are whitelist-validated (side, type, instrument)
- [ ] JSON body size is capped (`MaxBytesReader`)
- [ ] No SQL injection vectors (N/A — no database)
- [ ] No command injection vectors (no shell exec)
- [ ] Path traversal prevented in instrument-to-filename mapping

### Network
- [ ] TLS enabled with minimum version 1.2
- [ ] HTTP server timeouts configured (read, write, idle)
- [ ] CORS restricted to known origins
- [ ] HSTS header set (`Strict-Transport-Security`)
- [ ] No sensitive data in URL query parameters
- [ ] WebSocket connections authenticated before upgrade

### Rate Limiting
- [ ] Per-client rate limits enforced
- [ ] Separate limits for read vs write operations
- [ ] Pre-auth rate limiting for unauthenticated flood protection
- [ ] Client tracking memory is bounded (idle cleanup)
- [ ] Rate limit headers returned (`X-RateLimit-Remaining`, `Retry-After`)

### Data Protection
- [ ] WAL files have restrictive filesystem permissions
- [ ] Config files with secrets have 0600 permissions
- [ ] API key secrets are never logged
- [ ] No sensitive data in error responses to clients
- [ ] WAL entries have integrity checksums
- [ ] Snapshot files have tamper-detection signatures

### Operational
- [ ] Health endpoint does not leak internal state
- [ ] Graceful shutdown drains in-flight requests
- [ ] Panic recovery middleware prevents crash-on-bad-input
- [ ] Log output does not contain secrets or full request bodies

---

## Code Review Security Checklist

Use this when reviewing PRs or new code:

### Data Handling
- [ ] No `float64` used for price/quantity comparison or storage
- [ ] Integer overflow checked for price/qty conversions
- [ ] Untrusted strings sanitized before use in file paths
- [ ] Map access checked (no nil pointer dereference on missing key)
- [ ] Slice bounds checked (no panic on out-of-range index)

### Concurrency
- [ ] No shared mutable state across instrument workers
- [ ] Locks acquired in consistent order (no deadlock risk)
- [ ] Channel operations have timeout or default case
- [ ] No goroutine leaks (every `go func()` has a shutdown path)
- [ ] `sync.Pool` objects are zero-initialized on Get()

### Error Handling
- [ ] Errors are not silently swallowed
- [ ] Error messages don't leak internal implementation details to clients
- [ ] WAL write failures prevent the mutation (not just logged)
- [ ] Recovery/replay errors are fatal (not silently ignored)

### Cryptography
- [ ] HMAC-SHA256 with 256-bit keys minimum
- [ ] No custom crypto implementations
- [ ] Secrets decoded from hex/base64, not stored as plaintext strings
- [ ] Time comparison uses monotonic clock where possible

---

## Incident Response Template

```markdown
## Incident: [Title]
**Severity**: Critical / High / Medium / Low
**Detected**: [timestamp]
**Resolved**: [timestamp]

### What happened
[Brief description]

### Impact
- Orders affected: [count]
- Trades affected: [count]
- Data loss: [yes/no, details]
- Client impact: [description]

### Root cause
[Technical explanation]

### Timeline
- [time] — First indicator
- [time] — Investigation started
- [time] — Root cause identified
- [time] — Fix deployed
- [time] — Verified resolved

### Remediation
- [ ] Immediate fix applied
- [ ] WAL replay verified state consistency
- [ ] Affected clients notified
- [ ] Post-incident review scheduled

### Prevention
- [ ] [Action item 1]
- [ ] [Action item 2]
```

---

## Threat Scenarios

### 1. Order Flood Attack
**Attack**: Authenticated client submits millions of orders to exhaust server resources.
**Detection**: Monitor orders/sec per client via metrics.
**Mitigation**: Per-client rate limiting (already implemented), max open orders per client (TODO).

### 2. Price Manipulation via Layering
**Attack**: Client places large orders at extreme prices to move the apparent market, then cancels.
**Detection**: High cancel-to-trade ratio per client.
**Mitigation**: Order-to-trade ratio limits, minimum resting time for large orders.

### 3. WAL Replay Attack
**Attack**: Attacker with filesystem access modifies WAL files to inject fake orders on recovery.
**Detection**: CRC/HMAC integrity check on WAL entries.
**Mitigation**: Sign WAL entries with server-side key, validate on replay.

### 4. Timestamp Manipulation
**Attack**: Client submits requests with timestamps in the future to extend the replay window.
**Detection**: Already handled — `abs(age)` check rejects future timestamps too.
**Mitigation**: Current implementation is correct.

### 5. Memory Exhaustion via Deep Book
**Attack**: Client creates thousands of price levels by placing orders at unique prices.
**Detection**: Monitor `bid_levels` + `ask_levels` per instrument.
**Mitigation**: Cap max price levels per instrument, max orders per client.
