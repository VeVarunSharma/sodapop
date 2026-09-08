# Why the agent instructions are layered

This layout adapts OpenAI's
[harness-engineering article](https://openai.com/index/harness-engineering/):
start with a small navigation entry point, keep durable knowledge in the repository,
and make important constraints executable. It does not copy the article's entire
documentation tree or introduce a fleet of runtime agents.

## What belongs where

| Location | Content | Reason |
| --- | --- | --- |
| [Root AGENTS.md](../AGENTS.md) | Global boundaries, development loop, task-to-guide map | Every task needs orientation, not every package's details. |
| Scoped `AGENTS.md` files linked from the root | Local rules, their reasons, code/test entry points, focused commands | Load the constraints for the boundary being changed. |
| Existing documents in `docs/` | Architecture, authentication, qualification, operational detail | Keep explanations discoverable without injecting a manual into every task. |
| [Copilot entry point](../.github/copilot-instructions.md) | Pointer to the root and applicable scopes | Retain the existing entry point without maintaining duplicate policy. |
| Source and focused tests | Actual behavior and enforceable contracts | Prose alone cannot guarantee identity isolation, cancellation, or permissions. |

These files are instructions for agents editing Sodapop, not definitions of
independent agents or additional application integrations.

## How to use the map

Read the root, then guides on the path to the file being changed. A UI-only change
needs the UI guide, not all authentication and packaging details. An account-switch
change crosses app/auth/engine/UI boundaries and needs each affected guide.
Scoped rules supplement the root; they never weaken its safety constraints.
Follow links when their topic becomes relevant, rather than loading the whole tree.

Automatic instruction loading varies by client. A Markdown link does not itself
load a document, so both entry points explicitly tell the agent to read relevant
guides before editing. In Sodapop specifically,
[projectInstructions](../internal/engine/session_config.go) loads only the root
`AGENTS.md` and `.github/copilot-instructions.md`, with bounded in-project reads.
Nested guides are additional file-tool reads. Runtime instruction discovery stays
disabled; adding these files does not change the loader or permission policy.

There is deliberately no instruction file for every source file or asset directory.
A package guide's code links handle file-level navigation. Add a new scope only
when it has distinct, recurring constraints that would otherwise burden unrelated
tasks; do not add pass-through guides or restate the root.

## Writing a useful rule

State the constraint, explain the failure it prevents, and point to implementation
and tests. For example, binding `TokenSource` to `AccountID` prevents a later login
from changing an existing engine's identity; "be careful with auth" does not tell
an agent what to preserve. Local guides use this constraint-and-reason pattern.

Keep detailed designs in linked documents. Record a newly discovered recurring
failure in a focused regression test where possible, then add only the durable
lesson to the narrowest guide. Do not accumulate task histories or generic advice.
When code changes an invariant, update its guide and linked explanation in the same
change; remove obsolete rules rather than appending contradictory exceptions.

## Keeping the layout honest

[TestAgentGuidance](../scripts/agent_guidance_test.go) runs in the existing Go suite:
it requires every scoped guide to be indexed by the root, checks inline local-link
targets, and limits the root to 100 lines / 8 KiB, scoped guides to 60 lines / 4 KiB,
and the Copilot pointer to 12 lines / 1 KiB. These are ceilings, not writing targets.
Move detail into linked documents rather than hiding it in oversized lines.

Run `go test ./scripts -run '^TestAgentGuidance$'` from the repository root after
editing guidance. It also runs through `make test`, `make check`, and existing CI;
no new dependency or workflow is needed. External URLs and heading anchors are not
checked, and a valid link cannot prove that a rule still matches the code.
Review semantic accuracy against the linked implementation and tests during changes.
