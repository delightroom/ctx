# Security model

`ctx` is intended for a private, access-controlled Tailscale tailnet. It is not
an untrusted cross-organization sharing service.

## Boundaries

- The host runs the locally installed `ctx` binary.
- The backend listens on `127.0.0.1` only.
- Tailscale Serve supplies tailnet-only HTTPS and ACL enforcement.
- Consumers receive normalized JSON only.
- Dashboard previews render normalized message text, not native session
  envelopes, tool inputs, or tool results. Control sequences are stripped and
  redaction is applied again before display.
- The paged transcript exposes tool names and result boundaries, but never raw
  tool arguments or tool-result bodies.
- `ctx continue` creates a new session from a quarantined prompt. It does not
  import or execute the host's native session.

## Residual risks

Remote transcript content can still influence a consuming model. Quarantine
framing reduces accidental instruction adoption but cannot eliminate prompt
injection. Consumers must verify repository state and retain their normal
approval and sandbox policies.

Pattern redaction has false positives and false negatives. Do not host sessions
that handled credentials or other sensitive material.

Opening a local preview does not publish it or call an LLM. Opening a shared
preview fetches that feed's existing digest from its provider over the
tailnet. The preview is fetched from the exact origin discovered for the row
and must match its advertised identity and revision. Redirects are not
followed. Anyone who can see the dashboard can see its short message excerpts.

Stopping `ctx host` prevents future reads. It cannot revoke copies already
downloaded by an authorized peer.
