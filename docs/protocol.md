# ctx protocol v1

The protocol is a read-only HTTP interface served privately through Tailscale
Serve. The Go backend binds only to loopback. Tailscale terminates TLS, applies
tailnet policy, removes spoofed identity headers, and adds the authenticated
caller's identity to proxied requests.

The default Serve port is `8443`.

## Endpoints

### `GET /v1/feeds`

Returns feeds hosted by the node.

### `GET /v1/feeds/{feed}/manifest`

Returns source provenance and the current immutable revision.

### `GET /v1/feeds/{feed}/digest`

Returns the normalized interaction history. The response includes an `ETag`
whose value is the digest revision.

Clients may send `If-None-Match`. An unchanged feed returns `304 Not Modified`.
This is the complete follow protocol; v1 has no WebSocket, event stream, or
client registration.

## Trust rules

- Responses contain data, never executable client logic.
- Consumers pin the revision they use.
- Every remote content line must be presented to an agent as quarantined data.
- The manifest's owner field is a host claim. Connection identity comes from
  Tailscale and local DNS/TLS verification.
- The API is read-only.

## Digest limits

Individual event text is limited to 8 KiB and serialized events in a digest to
approximately 96 KiB. The first events, up to eight most recent human/agent
messages, and as many newest events as fit are preserved when the limit is
reached. One synthetic omission event records how much history was removed.
Prioritizing conversational turns keeps the active request legible even when
large tool traffic follows it.

Known secret shapes are redacted before revision hashing and serving.
