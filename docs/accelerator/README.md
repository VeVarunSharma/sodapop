# Sodapop accelerator preparation pack

## Readiness summary

| Field | Value |
| --- | --- |
| Build | Sodapop — Approval-Aware Terminal Coding Companion |
| Repository / demo | [Source](https://github.com/VeVarunSharma/sodapop), [website](https://sodapop.sh), [presenter guide](demo.md) |
| Customer / industry | Reusable / cross-industry software engineering |
| Primary persona | Developer; secondary: platform engineer and engineering lead |
| Desired readiness | Catalog-ready |
| Current assessment | Demo foundation; live customer demo, field, and catalog readiness remain conditional |

This pack supplies a synthetic scenario, presenter scripts, engagement motion,
architecture visual, and evidence templates. It does not establish successful
live execution. Existing signed-out GIFs show prepared changes, not AI output.
Workflow definitions, qualification harnesses, and draft metadata are not proof
that their gates passed.

### Top three fixes before sharing

1. Obtain explicit authorization and qualify installation, interactive sign-in,
   Copilot access, an approved change, observed tests, and review on a declared
   platform/model/version. The [live harness](../live-qualification.md) is a
   separate narrow check, not a substitute for the complete TUI journey.
2. Have another presenter rehearse the [sample](sample/README.md) using only the
   [facilitator guide](demo.md); capture sanitized genuine outcome assets.
3. Confirm support ownership, private security reporting, catalog eligibility,
   submission schema, and required taxonomy before publication.

## Use this pack

| Asset | Purpose |
| --- | --- |
| [Synthetic order-total exercise](sample/README.md) | Customer request, baseline, acceptance cases, safe recreation |
| [Demo guide](demo.md) | Setup/deployment links, journey, timed scripts, failure handling, media inventory |
| [Engagement guide](engagement.md) | Customer story, discovery, workshop, pilot scorecard, adaptation |
| [Evidence template](evidence.md) | Qualification, media, independent rehearsal, publication decisions |
| [Architecture diagram](../architecture.md#trust-boundary-diagram) | Local/external services, state, credentials, optional MCP |
| [Customer request slide](request.svg) | Public-safe synthetic request and acceptance criteria |
| [Draft metadata](../../accelerator.yaml) | Repository preparation data; not a validated submission |

Sodapop is a local application, not an Azure-hosted service. No Azure resource
deployment is needed. Use [installation](../installation.md),
[source development](../development.md), and
[distribution](../distribution.md) for setup, integrity, native delivery,
upgrade, and uninstall boundaries. Do not disable OS protections or replace
Sodapop's existing OAuth registration to make a demo work.

## Catalog verification

The requested catalog is
<https://github.com/mcaps-microsoft/accelerators-catalog>.
On **2026-10-18**, GitHub's repository-contents API returned **404** for that
repository. This may reflect access restrictions or an incorrect/moved URL;
it does not prove the catalog does not exist.

Ask the catalog owner for the authoritative repository/access, eligibility for
an independent GitHub-focused local application, contribution instructions,
metadata schema, taxonomy, review process, and required licensing/support fields.
Then map and validate `accelerator.yaml` against those requirements and record
the schema revision in the evidence record. No catalog-specific schema or
acceptance is claimed here. Do not add Azure services purely for positioning.

## Repository preparation checklist

Checked items mean materials exist, not that the build is qualified.

- [x] README customer value, ASCII art, quickstart, journey, boundaries, CTA.
- [x] Architecture diagram with local/external and credential/state boundaries.
- [x] Five-/fifteen-minute scripts, failure branches, setup and cleanup guidance.
- [x] Synthetic request, source, baseline tests, acceptance criteria.
- [x] Reuse existing signed-out GIF/PNG assets with truthful captions.
- [x] Discovery, workshop, adaptation guidance, pilot scorecard.
- [x] Evidence template and draft accelerator metadata.
- [x] Existing root/scoped Copilot guidance and credential-free/native workflows.
- [ ] Independently rehearse fresh-machine setup and the full live journey.
- [ ] Capture genuine context, approval, changed-code, tests, and diff screenshots.
- [ ] Produce captioned live walkthrough, static alternatives, and sanitized recap.
- [ ] Record passing CI/checks for the exact shared revision and environment.
- [ ] Review source, fixtures, media, logs, distribution payloads, dependency
      advisories, licenses, and publication history for sensitive content.
- [ ] Confirm support/security owners and a working private reporting route.
- [ ] Validate metadata against the authoritative catalog schema and submit through
      its documented process; retain the acceptance reference.

Never lower coverage gates, treat checks skipped for lack of credentials as live
success, or publish raw credentials/provider payloads as evidence.

## Readiness gates

| Gate | Required evidence |
| --- | --- |
| Demo-ready | One declared environment; independent reproduction; real outcome; safe, accurately labeled fallback |
| Field-ready | Another SE/CSA can facilitate without the author; support, security, adaptation, and pilot materials verified |
| Catalog-ready | Field-ready evidence plus confirmed eligibility, validated metadata, working links, ownership, and catalog acceptance |

Progress only when observations meet the gate. Keep failures and unverified
requirements visible in [evidence](evidence.md).

### Preparation checks observed on 2026-10-18

On Linux amd64 with Go 1.27.1, the preparation changes passed the focused
sample tests and `make check` with both live flags explicitly `0`. The coverage
ratchet passed at 85.6% against the unchanged 84.8% baseline, package floors
passed, and the race suite and `go vet` passed. These are local observations,
not GitHub Actions or live Copilot qualification.

The draft YAML and request-slide XML parsed, referenced metadata files existed,
and changed-file secret scanning reported no secrets. This is not catalog-schema
validation or a historical security audit. The automated review binary was
unavailable; an independent read-only reviewer found a presenter checkout-path
portability issue, which was corrected. CodeQL was skipped for the
documentation/static assets/teaching-fixture change; no production runtime logic
changed. Keep native installation, interactive sign-in, real model execution,
live captures, independent rehearsal, and catalog acceptance pending.

## Support and security handoff

Community support starts with [troubleshooting](../troubleshooting.md).
For nonsensitive issues use
[repository issues](https://github.com/VeVarunSharma/sodapop/issues), including
version, platform/architecture, installation channel, expected/actual behavior,
and sanitized reproduction steps. No response-time commitment, commercial
support, or Microsoft support entitlement is implied.

Before field publication the repository owner must name a maintenance/support
owner, escalation contact, supported versions, response expectations, and review
cadence in the evidence record.

**Security reports are not ordinary public issues.** The owner must verify and
publish a private reporting route. If GitHub private vulnerability reporting is
enabled, use the repository's Security → Report a vulnerability flow; its
availability has not been verified here. If unavailable, arrange a private
channel with the owner before transmitting details. Do not put exploit details,
tokens, device codes, private source, or personal information into public issues.

This pack does not claim a new security audit or a full historical secret scrub.
Keep first-party MIT licensing and the bundled runtime's separate terms and
[notices](../../THIRD_PARTY_NOTICES.md). Sodapop is independent, not the official
GitHub Copilot CLI; Microsoft endorsement is not implied.
