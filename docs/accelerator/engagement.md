# Customer engagement preparation

**Status: draft workshop/pilot material.** This is a proposal and adaptable
story, not a customer reference, measured outcome, Microsoft endorsement, or
catalog acceptance. No customer metrics or live qualification are asserted.
Pair with the [facilitator guide](demo.md), [sample](sample/README.md), and
[architecture](../architecture.md).

## Illustrative customer story

A development team wants to reduce the friction between understanding a small
bug, agreeing a fix, and reviewing evidence in the terminal. In a proposed
workshop, the team uses Sodapop on a synthetic orders project whose baseline tests
pass while missing a quantity-calculation bug. A developer asks for a plan,
reviews each permission request, and evaluates a scoped fix with newly added
regression tests and actual test output. The intended learning is whether this
reviewable workflow fits the team's practices; productivity, quality, and adoption
benefits remain hypotheses until an authorized pilot measures them.

## Opener

“Where does a small code change slow down today: understanding the context,
deciding what to change, or gathering evidence for review? We'll use a tiny
synthetic bug to explore a terminal-first workflow with explicit approvals, then
decide whether a narrowly scoped pilot is worth measuring. This is not a promise
of autonomous delivery, a sandbox, or a production-readiness assessment.”

## Discovery questions

1. Which repeatable coding or maintenance task creates the most review friction,
   and who owns its acceptance criteria?
2. How do you currently measure elapsed time, developer effort, rework, and review
   quality for that task?
3. Which languages, repository sizes, operating systems, and terminal workflows
   must a representative pilot support?
4. What data classifications or intellectual-property rules restrict code,
   prompts, logs, screenshots, or model-service access?
5. Which accounts have eligible Copilot access, and who approves the existing
   Sodapop OAuth application, organization policies, and any model usage/charges?
6. Who may approve file edits and unsandboxed shell commands, and what requests
   must always be denied or escalated?
7. What tests and reviewer evidence determine acceptance, and how will you record
   a failed, canceled, or unverified action without masking it?
8. What measurable result, operational constraint, or stop condition would make
   you expand, revise, or end the pilot, and who makes that decision?

## Workshop proposal

**Audience:** a developer, a reviewer/technical lead, an account/policy owner, and
a facilitator. Nominate a business sponsor and evidence custodian when proposing
a pilot. Confirm accessibility needs and recording consent beforehand.

**Suggested 45-minute agenda:**

| Time | Activity | Deliverable |
| --- | --- | --- |
| 0–10 min | Discovery and consent; confirm eligible accounts, data policy, and allowed actions. | Agreed scope and explicit live-usage decision. |
| 10–25 min | Authorized [15-minute demo](demo.md#fifteen-minute-flow), or labeled signed-out fallback. | Observed result and evidence gaps, not a simulated success. |
| 25–35 min | Map a comparable team task to inputs, context, workflow, output, and next action. | Candidate task, exclusions, acceptance tests, approval owner. |
| 35–45 min | Set baseline method, targets, stop conditions, and follow-up. | Draft scorecard and named owners/date. |

Preparation includes a supported local installation, the disposable sample,
readable terminal, and an owner-approved authentication path. Follow
[installation](../installation.md), [authentication](../authentication.md), and
[local development](../development.md); no hosted deployment is implied.
Live prompts and recordings require explicit authorization separate from pack
preparation. If access or consent is missing, use the signed-out UI assets and
source walkthrough, disclose the limitation, and do not mark a live demo complete.

## Bounded pilot proposal

Agree the pilot window with the customer rather than presenting an existing
commitment. Select a few comparable, low-risk tasks and
an ordinary-workflow baseline before using Sodapop; record task complexity,
developer familiarity, environment, and interruptions so comparisons are not
misleading. Use synthetic or explicitly approved non-sensitive code first.

- **Scope:** `[task/repository subset]`, `[participants]`, `[platforms]`,
  `[approved model]`, `[dates]`, and `[usage budget]`.
- **Account/data gate:** named owner approves Copilot eligibility, Sodapop's
  existing OAuth registration, organization policies, permitted data, retention,
  and whether any recording is allowed. Never collect tokens or private codes.
- **Operating gate:** Autopilot off; explicit permission review; no automatic
  staging, commits, pushes, retries, or cleanup. Planning is advisory. Approved
  shell commands are unsandboxed, and cancellation does not undo edits.
- **Acceptance gate:** independent reviewer checks scoped changes, new regression
  coverage, and actual test outcomes. Assistant summaries alone do not pass.
- **Stop/escalate:** unexpected sensitive data, out-of-scope access, unexpected
  edits, unclear permission requests, unresolved access/policy failures, or usage
  beyond the agreed limit. Record the event, stop work, and obtain an explicit
  decision; do not silently replay failed/canceled tasks.
- **Decision gate:** sponsor and technical owner compare the scorecard and
  limitations at `[review date]`; expand, revise, or stop. No guaranteed savings.

### Pilot scorecard

All values below are **pending**. Agree numeric targets and collection methods
before the pilot; do not backfill targets after seeing results. Report sample
size, failures, and task differences alongside aggregates.

| Measure | Baseline / method to agree | Target to agree | Observed result | Owner |
| --- | --- | --- | --- | --- |
| Time to reviewer-accepted change | Pending; timestamp comparable tasks from accepted scope to review acceptance, including waiting/rework. | Pending; agreed improvement or acceptable bound. | Pending; not measured. | Developer + reviewer |
| Hands-on effort | Pending; record developer/reviewer minutes separately using the same method in both workflows. | Pending; agreed effort bound. | Pending; not measured. | Technical lead |
| Correctness and regression coverage | Pending; record acceptance tests, pass/fail outcomes, and missing cases before the pilot. | Pending; explicit required cases and checks. | Pending; not measured. | Test owner |
| Review rework | Pending; count substantive review corrections per comparable task. | Pending; agreed maximum or improvement. | Pending; not measured. | Reviewer |
| Permission/scope handling | Pending; log requests, decisions, unexpected actions, and escalations without secrets. | Pending; define prohibited events and stop criteria. | Pending; not exercised. | Policy owner |
| Reliability and recovery | Pending; track completed, failed, canceled, and unverified tasks plus recovery effort. | Pending; agreed completion/recovery bounds. | Pending; not measured. | Facilitator |
| Usage and cost | Pending; approved service usage source and recording granularity; separate estimates from billed evidence. | Pending; explicit usage/cost cap. | Pending; not measured. | Account/budget owner |
| Developer/reviewer fit | Pending; consistent short survey on usefulness, trust, accessibility, and friction. | Pending; agreed adoption criterion. | Pending; not surveyed. | Sponsor |

Preserve raw approved evidence and denominators. A single successful synthetic
task is not a statistically meaningful productivity claim. A skipped live test,
passing credential-free suite, or prepared GIF is not evidence of live account,
model, or production performance. See the precise limits of
[live qualification](../live-qualification.md).

## Adaptation and anonymization templates

### Customer-ready story, only after approval

> `[Approved organization alias]` needed to improve `[specific workflow]` under
> `[review/data constraints]`. During `[authorized dates]`, `[participant roles]`
> evaluated Sodapop on `[approved task scope]` against `[baseline method]`.
> Reviewers observed `[measured result with denominator and evidence reference]`;
> `[failures, limitations, and unverified items]` remain explicit.
> `[Named decision-making role]` chose `[expand/revise/stop]` and the next action
> is `[owner, task, and date]`.

If results do not yet exist, keep the illustrative story and label the engagement
“proposed.” Never turn placeholders into implied customer endorsements.

### Adaptation brief

```text
Audience and accessibility needs:
Customer-approved alias and reference/quotation permission:
Workflow pain and comparable baseline task:
Allowed inputs/context and prohibited data:
Local platform, app version, approved model, and account-policy owner:
Acceptance tests and reviewer:
Allowed edits/commands, permission owner, usage cap, and stop conditions:
Evidence location, redaction reviewer, retention/deletion owner:
Baseline / pre-agreed targets / observed results (pending until measured):
Decision owner, next action, and follow-up date:
```

Replace organization, person, repository, project path, branch, internal URL, and
ticket identifiers with approved aliases. Remove tokens, device codes, OAuth
responses, customer code, sensitive prompts/output, and account identities from
shared artifacts; cropping a screenshot is insufficient if metadata, transcripts,
or other frames still expose them. Review terminal titles, sidebars, diffs, and
shell paths. Obtain written approval for quotations, logos, and external sharing;
retain an internal evidence reference rather than publishing raw service payloads.

## Call to action

**Schedule a scoped discovery/workshop with one developer, one reviewer, and the
account/policy owner.** Bring one approved candidate task, its acceptance tests,
and the current review process. Leave with a named pilot owner, baseline method,
pre-agreed targets, live-usage decision, and review date—not a production rollout
commitment.

The intended [accelerators catalog](https://github.com/mcaps-microsoft/accelerators-catalog)
returned GitHub API 404 during the 2026-10-18 lookup; its submission rules and readiness requirements
cannot be assumed. This pack remains a preparation draft until maintainers
confirm access, eligibility, submission requirements, and any remaining evidence.
