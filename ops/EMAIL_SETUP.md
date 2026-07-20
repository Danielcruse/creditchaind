# Live outbound email setup

This is the operator-facing setup guide for the SMTP email layer in
`api/email.go`. The code paths that use it - wallet-recovery codes
(`api/recovery.go`) and hub-attention notifications (`api/hub_rate.go`) -
are already fully wired; nothing in this codebase needs to change to turn
real email on. What's missing is the SMTP credentials themselves, which
have not been set on either node.

**Nothing changes if you don't do this setup.** All four `SMTP_*` env vars
unset (the current state on both nodes) means `smtpConfigured()` returns
false, and every call site falls back to its existing testnet-placeholder
behavior (recovery codes returned directly in the API response, hub
notifications silently skipped) - exactly as it does today.

## 1. Pick an SMTP provider

Any standard SMTP server with STARTTLS + AUTH PLAIN works - `sendEmail`
(`api/email.go`) uses only the Go standard library (`net/smtp` +
`crypto/tls`), no provider-specific SDK. Reasonable options, cheapest
first:

- **Gmail** (fastest to set up, fine for low volume): a Google Account
  with 2-Step Verification enabled, then generate an **App Password**
  (Google Account -> Security -> App passwords) - do not use the account's
  real login password. `SMTP_HOST=smtp.gmail.com`, `SMTP_PORT=587`.
- **A transactional provider** (SendGrid, Mailgun, AWS SES) - better
  deliverability and sending limits at real user volume, each gives you
  an SMTP username/password pair from their own dashboard once you verify
  a sending domain. Worth moving to once recovery/notification email
  volume actually matters; not required to turn this on today.

Whichever you pick, you need four values: host, port, a username, and a
password - plus the "From" address you want recipients to see.

## 2. Set the SMTP env vars on node1

Same credential-handling pattern used for the Postgres password and S3
backup secret (see `ops/POSTGRES_SETUP.md`): never paste the password
directly into an SSH command - write the env block to a local file, `scp`
it over, then `sudo` it into place, rather than leaving it in shell
history on either end.

Add to `/etc/systemd/system/creditchain.service`'s `[Service]` section
(same file already holding `NETWORK_SECRET`/`ADMIN_TOKEN`):

```
Environment=SMTP_HOST=smtp.gmail.com
Environment=SMTP_PORT=587
Environment=SMTP_USERNAME=your-address@gmail.com
Environment=SMTP_PASSWORD=your-16-char-app-password
Environment=SMTP_FROM=your-address@gmail.com
```

(`SMTP_PORT` is optional - `sendEmail` defaults to `587` if unset.)

Then:
```
sudo systemctl daemon-reload
sudo systemctl restart creditchain.service
sudo journalctl -u creditchain.service --no-pager -n 20
```

There's no explicit "SMTP enabled" startup log line today (unlike
`DATABASE_URL`'s dual-write log) - `smtpConfigured()` is checked fresh on
each call site instead. Confirm it's live via step 3.

## 3. Verify it's actually sending

Trigger `HandleRequestEmailCode` (Settings -> Recovery Email -> Request
Code on `outernet.html`, or `HandleRequestRecoveryCode` from the Login
page's recovery flow) with a real email address you control. With SMTP
configured, the response no longer includes a `code` field (compare
`api/recovery.go`'s two branches) - you should instead receive an actual
email within a few seconds. If it doesn't arrive:

- `journalctl -u creditchain.service --no-pager | grep "failed to send"` -
  `sendEmail`'s errors (dial/auth/STARTTLS failures) are logged here, not
  surfaced to the caller (the API always returns `code_issued` either way,
  by design - it shouldn't leak whether the SMTP send itself worked to an
  unauthenticated caller on the recovery-request path).
- Gmail specifically: confirm the App Password (not the account password)
  is what's in `SMTP_PASSWORD`, and that 2-Step Verification is actually
  on for that account - Gmail rejects AUTH PLAIN with a normal password
  otherwise.

## 4. What this deliberately does NOT do yet

- **No retry/queue.** `sendEmail` is fire-and-forget (`go func() { ... }`)
  from each call site - a transient SMTP failure just logs and drops the
  message, the user gets no automatic retry. Acceptable today since both
  call sites (recovery codes, hub notifications) are re-triggerable by the
  user/hub re-requesting; would need reconsideration if a future email
  became one-shot-only (e.g. a password-reset-style link).
- **No rate limiting on the send side.** `HandleRequestEmailCode`/
  `HandleRequestRecoveryCode` aren't specifically throttled beyond the
  existing global per-IP rate limit (`corsMiddleware`) - a user could
  request many codes in quick succession, each firing a real SMTP send
  once this is configured. Low risk at current scale, worth revisiting
  if email costs or provider sending limits become a factor.
- **No HTML/branded templates.** Every email is `sendEmail`'s plain-text
  body as-is - functional, not polished. A real templating layer is a
  cosmetic follow-up, not a security or correctness gap.
