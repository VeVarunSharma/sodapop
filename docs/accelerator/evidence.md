# Qualification and publication evidence template

Copy this template into the team's approved evidence system; do not commit
customer or account information here. Values below are **unverified**, not a
record of a successful run. Attach only sanitized artifacts approved for sharing.

## Run identity

| Field | Record |
| --- | --- |
| Date (UTC) | Pending |
| Source commit / release / executable version | Pending |
| SDK/runtime versions and archive/binary digests | Pending |
| Installation channel and origin/attestation verification | Pending |
| OS / architecture / terminal / Go version for sample | Pending |
| Selected exact model ID | Pending |
| Organization policy and live-usage authorization confirmed | Pending; approval reference only, no account identity |
| Independent presenter rehearsal | Pending; no personal details in public copy |

## Observations

Record each command/action, expected and observed result, sanitized evidence
reference, and **passed / failed / unverified**. A model assertion is not evidence
of command execution. A runtime handshake does not prove authentication; a
successful saved-account harness run does not prove interactive device sign-in.

| Gate / action | Expected | Status / evidence |
| --- | --- | --- |
| Archive integrity and publisher verification | Trusted origin and matching exact bytes | Unverified |
| Installed help/version/runtime checks | Correct native executable and local handshake | Unverified |
| Interactive `/login` | Sodapop-owned client and separate Copilot connection | Unverified |
| Model selection and initial request | Actual authorized response using synthetic context | Unverified |
| Context and approvals | Intended project; appropriate decisions; Autopilot off | Unverified |
| Quantity regression | Added acceptance tests fail before the fix | Unverified |
| Approved edit | Only intended implementation/tests change | Unverified |
| Validation | Added acceptance tests and existing tests visibly pass | Unverified |
| `/taste-test` | Observed passed/failed/unverified evidence distinguished | Unverified |
| `/diff session` and whole-tree review | Intended changes; baseline exclusions disclosed | Unverified |
| Explicit `/resume` | Correct account/project history, no action replay | Unverified |
| Separately authorized live harness | Narrow documented assertions, uncached run | Unverified |
| Credential-free `make check` | Coverage ratchet, race tests, vet pass | Unverified |
| Independent fresh-machine rehearsal | Setup and scenario completed from docs alone | Unverified |

Keep normal checks' live flags at `0`. Execute live qualification only after
separate explicit authorization, following [the existing guide](../live-qualification.md).
Never infer qualification for other models/platforms from one successful run.

## Publication assets

Use the [demo inventory](demo.md#media-and-fallback) for capture requirements.
For each artifact record scope (live / signed-out UI / diagram), date, revision,
caption/alt text, sanitization review, and public-safe reference.

- [ ] Customer request and acceptance-criteria slide.
- [ ] Architecture with local/external boundaries.
- [ ] Actual context discovery and approval decision.
- [ ] Actual changed code, executed test results, and reviewed diff.
- [ ] Captioned five-minute walkthrough and static alternatives.
- [ ] Outcome recap including failures, unverified items, and next action.
- [ ] Existing signed-out fallback clearly labeled; never passed off as live AI.

Do not record device authorization. Inspect every frame, terminal title,
notification, sidebar, path, transcript, and audio track. Exclude tokens, device
codes, account identities, personal paths, customer source, environment contents,
and provider payloads. Do not upload raw qualification logs. Follow the live
harness's explicit restrictions on response/event capture.

## Reuse and publication decision

| Decision | Required record |
| --- | --- |
| Customer adaptation | Business vocabulary, task, policy, integrations, acceptance criteria |
| Pilot baseline / target / observed outcome | Use the [scorecard](engagement.md); no assumed benefit |
| Maintenance and support | Owner role, supported versions, escalation route, response expectations, review cadence |
| Private security reporting | Owner-verified route and verification date |
| Security / license scrub | Scope, revision, tools/review results, unresolved findings; distinguish changed-file scan from history audit |
| Catalog | Authoritative URL/schema revision, eligibility, validation, submission and acceptance references |
| Readiness decision | Demo / field / catalog gate met or blocked, with reasons |

All ownership and catalog decisions remain pending until verified. Record
unresolved blockers with an accountable role and next action, not fabricated
completion dates or promises. Do not mark the catalog gate complete simply
because `accelerator.yaml` exists.
