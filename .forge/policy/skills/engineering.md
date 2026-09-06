# Darkbloom engineering

Read AGENTS.md and CONTRIBUTING.md before editing. Work only on the assigned scope.
Use isolated tests and the disposable local DATABASE_URL. Never contact production,
change versions, publish code, or obtain credentials. Preserve fresh attestation
requirements. Cover memory and PostgreSQL when changing persistence.

Read actual source before choosing a fix. Reproduce reported failures. Keep regression
fixtures intact. Add meaningful edge-case tests. Update affected documentation and
run its checks. Record what changed, observed results, and remaining uncertainty.
A completed agent run produces a candidate; a separate worker verifies it.

Trace every caller of a changed helper, including retries and speculative winners.
After provider handoff, use the matching retained provider for slot attribution;
a global registry lookup can block first-byte delivery behind unrelated writes.
Preserve frozen fault attribution. Force lock ordering in regression tests and
release synchronization gates on failure; repeated natural passes alone do not
disprove a concurrency bug.
Capture complete test output and the command exit status before displaying a
summary. Filtering to the last lines can discard the failing test and force an
expensive rerun just to recover the diagnosis.
Test fixtures own their asynchronous work: use distinct persisted and reconnecting
session identities, and cancel and join provider scripts before test teardown.
Preserve production assertions when repairing a flaky fixture.
