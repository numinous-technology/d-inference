# Provider reconnect reliability

Restore prior state only through the freshly verified Secure Enclave public key.
Serial numbers alone do not establish identity. A stale startup snapshot cannot
represent state earned after server startup. Exclude the registering session from
history lookup. Cover missing reputation rows and multiple reconnects.
Preserve fresh trust verification and avoid introducing unrelated routing changes.
Do not copy an old production patch; inspect the current implementation and solve
the failure demonstrated by the regression fixture.

Device metadata and earned reputation can have different history rows. Select
the newest matching metadata independently from the newest matching reputation.
Use the same deterministic timestamp and ID ordering in both stores. Store
lookup results must not expose mutable persisted buffers to callers. Tests shared
by memory and PostgreSQL must actually run their PostgreSQL subtests.
