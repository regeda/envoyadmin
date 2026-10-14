# Secrets tab

Date: 2026-10-14
Status: proposed
Problem: Techlog `evt_MV80409QZTZP`

## Problem

Envoy admins checking whether Envoy is in sync with the control plane cannot see
in envoyadmin which secrets Envoy has loaded or at which version. Nothing covers
this today. They need a **Secrets** tab that lists each secret with its name and
version, never its content, and shows which clusters and listeners use it.

## Goals

- A third tab, **Secrets**, next to Clusters and Listeners.
- Per secret: name, version (`version_info`), state (active, warming or static),
  last updated, kind (TLS certificate, validation context, session ticket keys,
  ...), and, for certificates, serial number, SANs and expiry with days left.
- Per secret: the clusters and listeners that reference it ("used by").
- Per cluster and listener: the secrets it references, with a marker for a
  referenced secret that Envoy has not loaded.
- Secret content is never fetched into the snapshot, stored or rendered.

## Non-goals

- Navigating from a "used by" entry to the cluster or listener (see rejected
  options).
- A Config tab for secrets.
- Any write operation against Envoy.

## Design

### Data and fetching (`internal/admin`, `internal/snapshot`)

`admin.Client` gains two methods:

- `SecretConfigs(ctx)` reads
  `/config_dump?resource=dynamic_active_secrets`, `dynamic_warming_secrets` and
  `static_secrets`. It decodes the dump as raw JSON, like `decodeClusterConfigs`
  and `decodeListenerConfigs`: unknown or malformed elements are skipped, never
  fatal. It keeps only name, `version_info`, `last_updated`, state and kind. The
  kind is the name of the one populated field of the secret (`tls_certificate`,
  `validation_context`, `session_ticket_keys`, ...). The secret body is dropped
  at decode time and is never part of any type this package returns.
- `Certs(ctx)` reads `/certs` and keeps serial number, SANs, expiry time and days
  until expiration per certificate.

`snapshot.Fetch` calls both in parallel with the existing calls, joins the
certificate facts onto the secrets and fills `Snapshot.Secrets` and
`Snapshot.SecretsErr`.

New snapshot types:

- `Secret{Name, State, Kind, VersionInfo, LastUpdated, Cert *CertInfo, UsedBy []Ref}`
- `CertInfo{Serial, SANs, NotAfter, DaysLeft}`
- `Ref{Kind, Name}` where Kind is cluster or listener.

`Cluster` and `Listener` each gain `Secrets []string`.

### "Used by" derivation (`internal/snapshot`)

The snapshot already holds each cluster's and listener's config JSON. A helper
walks that JSON generically and collects the `name` under any key ending in
`sds_secret_config` or `sds_secret_configs`. This covers TLS certificates,
validation contexts and combined validation contexts, upstream and downstream,
without hard-coding paths.

The result fills `Secret.UsedBy` and each cluster's and listener's `Secrets`. A
referenced name that is absent from the secrets dump is marked "not loaded",
which is the out-of-sync signal admins need.

### UI and docs (`internal/ui`, README, `internal/demo`)

- A third view kind, Secrets, with list and detail panes like the other tabs.
- List rows: state glyph, name, version. Warming and expired secrets sort first,
  then by name. `/` filters by name.
- Overview (`o`): name, state, kind, version, last updated, certificate facts
  (serial, SANs, expiry with days left) and the "used by" list.
- No Config tab. `c` on this tab shows a short note that secret content is never
  displayed.
- The cluster and listener Overviews gain a "Secrets" row listing the secrets
  they use, with the not-loaded marker where it applies.
- The tab label reads `Secrets (!)` when a secret is warming, a certificate is
  expired or the secrets dump failed.
- The demo fake Envoy serves sample secrets and a `/certs` response. The README
  documents the tab and its keys.

### Errors

A failed secrets dump never raises the global error banner. The tab shows an
inline "secrets unavailable: <reason>" message, as Listeners does. If only
`/certs` fails, the secrets are still listed and an inline note says that
certificate details are unavailable.

### Testing

- Decoder tests against a genuine capture from `envoyproxy/envoy:v1.31-latest`
  with file-based SDS secrets, added under `internal/admin/testdata` and
  described in its README like the existing captures.
- Snapshot tests for the certificate join, `UsedBy`, `Secrets` on clusters and
  listeners, and the not-loaded marker.
- Golden UI tests for the Secrets list, the Secrets Overview and the new
  "Secrets" rows.
- A test that renders a secret fixture containing a sentinel private key and
  asserts that the sentinel appears nowhere in the output.

### Open item (settled, see the amendment below)

How `/certs` entries map back to SDS secret names is not established. The real
capture settles it. If no reliable join exists, certificate facts are shown only
where they match a secret, and this spec is updated to say so.

## Amendment (after the Task 2 capture)

A genuine capture from Envoy 1.31.10 settled the open item: `/certs` reports an
SDS certificate with `path` `"<inline>"`, so its entries cannot be joined to a
secret by name (and two secrets sharing one certificate cannot be told apart).
The secrets dump, however, carries the public certificate PEM (only the private
key is `[redacted]`). The design therefore changes in these ways:

- There is no `Certs` client method, no `/certs` request and no `CertsErr`.
- The decoder reads serial, SANs and expiry from the first `CERTIFICATE` block of
  the secret's inline certificate chain (or trusted CA) and drops the PEM; the
  private key is never read. A file-based or unreadable certificate yields no
  certificate facts, and the secret is still listed.
- Days left are computed from the expiry at fetch time.
- The demo Envoy emits real generated certificates in its secrets dump instead of
  serving `/certs`.

"Secret content is never kept" still holds: only the facts survive decoding.

## Options considered and rejected

### Data source

1. **Dedicated fetches joined in `snapshot.Fetch` (chosen).** Two more HTTP calls
   per refresh. Follows the existing listeners pattern and keeps secret bodies
   out of the snapshot.
2. **One unfiltered `/config_dump` parsed once for everything.** Fewer calls, but
   it requires rewriting the cluster and listener decoders, the payload can be
   very large, and the secrets body would sit in memory. Rejected as a refactor
   beyond this problem.
3. **Derive secrets only from the references in clusters and listeners.** No new
   calls, but it cannot show versions, which is the core of the problem.
   Rejected.

### Additional information per secret

- **A. Config-dump basics** (state, last updated, kind). Cheap and consistent with
  the other tabs.
- **B. A plus certificate facts from `/certs`.** Useful for rotation checks.
- **C. B plus a Config detail tab with redacted content.** Rejected: it adds
  redaction risk to a tab that must never show content.

Chosen: A and B together. Certificate facts depend on the open item above.

### Links to clusters and listeners

- **A. Used by, display only (chosen).** The secret lists its users, and clusters
  and listeners list their secrets. Nothing is navigable.
- **B. Used by, navigable.** A key on a "used by" entry jumps to that cluster or
  listener. Rejected for now: it needs cross-tab selection state in `model.go`.
  A reasonable follow-up.
- **C. One-way only.** The secret lists its users and the other tabs stay
  untouched. Rejected: it does not answer "which secrets does this listener
  depend on?".
