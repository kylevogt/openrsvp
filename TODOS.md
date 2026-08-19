# TODOS

## Deferred Items

### Accessibility Audit (Post-Design System)
- **What:** Run a full WCAG 2.1 AA audit across all pages.
- **Why:** Design system color changes affect contrast ratios. DESIGN.md specifies AA-compliant pairs, but edge cases (muted text on subtle backgrounds, dark mode combinations) need automated verification.
- **Context:** Design preview showed good contrast (16.8:1 primary text, 4.5:1 primary on white). Run after design system ships and Playwright is set up. Consider integrating axe-core into Playwright tests.
- **Depends on:** Design system rollout complete + Playwright visual regression setup. **Both shipped in v1.5.0 — this TODO is now unblocked.**
- **Scope update:** Include setup wizard pages (/setup) and the new Account, unsubscribe, and guest-feedback surfaces in audit scope. The setup wizard shipped in v1.6.0 with form inputs, provider picker, and step navigation.
- **Added:** 2026-03-18 via /plan-eng-review
- **Updated:** 2026-06-10 (setup wizard shipped in v1.6.0; scope extended to v1.6.0 UI surfaces)

### Credential Encryption for Instance Config
- **What:** Add AES-256-GCM encryption for SMTP credentials stored in the `instance_config` table.
- **Why:** If a user backs up their SQLite database and the backup is exposed, SMTP credentials (passwords, API keys) are in plaintext. Encryption-at-rest protects against backup exposure. The running instance threat model is unchanged (attacker with filesystem access can read both .env and the key file).
- **Pros:** Better security posture for database backups. Standard practice for credential storage at rest.
- **Cons:** Adds key management complexity. Requires INSTANCE_SECRET env var or auto-generated `/data/.instance_secret` file. Backups can't be restored without the key.
- **Context:** Deferred during distribution playbook eng review. The current .env file also stores credentials in plaintext, so moving to DB doesn't change the threat model for running instances. Encryption matters most for backup exposure scenarios.
- **Depends on:** Setup wizard (Phase 1 of Distribution Playbook) must ship first. **Setup wizard shipped in v1.6.0 — this TODO is now unblocked.**
- **Added:** 2026-03-26 via /plan-eng-review

### Email Click-Tracking Redirect Endpoint (P2)
- **What:** Implement a click-tracking redirect endpoint so `EMAIL_CLICK_TRACKING_ENABLED` does something.
- **Why:** The `EMAIL_CLICK_TRACKING_ENABLED` config flag is reserved but unimplemented. Open tracking shipped in v1.6.0; click tracking needs a redirect endpoint that rewrites email links, records the click, then 302s to the destination.
- **Context:** Mirror the open-tracking pixel pattern. Keep it gated behind the existing flag (default off).
- **Added:** 2026-06-10
