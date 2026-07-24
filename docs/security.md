# Security model

`ctx` is intended for a private, access-controlled Tailscale tailnet. It is not
an untrusted cross-organization sharing service.

## Boundaries

- The host runs the locally installed `ctx` binary.
- The backend listens on `127.0.0.1` only.
- Tailscale Serve supplies tailnet-only HTTPS and ACL enforcement.
- Consumers receive normalized JSON only.
- `ctx continue` creates a new session from a quarantined prompt. It does not
  import or execute the host's native session.

## Residual risks

Remote transcript content can still influence a consuming model. Quarantine
framing reduces accidental instruction adoption but cannot eliminate prompt
injection. Consumers must verify repository state and retain their normal
approval and sandbox policies.

Pattern redaction has false positives and false negatives. Do not host sessions
that handled credentials or other sensitive material.

Stopping `ctx host` prevents future reads. It cannot revoke copies already
downloaded by an authorized peer.
