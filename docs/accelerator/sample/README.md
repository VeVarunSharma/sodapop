# Synthetic order-total maintenance exercise

**Intentionally defective teaching fixture, not production order processing.**
`TotalCents` currently ignores quantities. The existing tests cover empty carts
and single-unit lines only: passing them does not establish the requested
behavior. Keep the defect in this baseline; perform the demo fix in a disposable
copy, never in the Sodapop checkout.

## Customer request

> Our internal order preview undercounts carts containing several units of the
> same item. Explain the calculation, add regression tests, and correct the
> smallest relevant implementation. Show executed checks and a reviewable diff.
> Do not add dependencies, access external services, or commit/push changes.

All names, code, and examples are synthetic. The fixture is a library, not a
web application: `go test ./...` is its runnable experience. The assistant
interaction additionally requires installed Sodapop and authorized Copilot use.

## Inputs, context, and acceptance criteria

`Line` has an integer unit price in cents and an integer quantity. Inputs for
this exercise are nonnegative and bounded so every product and sum fits `int64`.
Negative values, overflow policy, discounts, tax, currency conversion, payment
processing, and UI changes are explicitly out of scope.

| Cart: `(unit price cents, quantity)` | Required total cents |
| --- | ---: |
| Empty / nil | 0 |
| `(125, 1)` | 125 |
| `(125, 3)` | 375 |
| `(125, 2), (250, 3)` | 1000 |
| `(125, 0)` | 0 |
| `(0, 4)` | 0 |

Add regression tests for repeated quantities, mixed lines, zero quantity, and
zero price. First observe the relevant tests fail against the baseline; then
approve the minimal fix and rerun all tests. Keep the existing baseline tests.
Expected outcome: every listed case passes, no new dependencies/files outside
the exercise, and a human-reviewed diff with observed test evidence.

## Create a disposable workspace

These commands are for the **operator**, not automatic Sodapop actions. Use a
POSIX shell (Git Bash on Windows), Git, and the Go version required by the source
checkout's `go.mod` or later. An installed native Sodapop itself does not need Go.
In another checkout, replace the absolute source path before running.

```sh
demo_dir="$(mktemp -d /tmp/sodapop-orders.XXXXXX)"
cp /home/runner/work/sodapop/sodapop/docs/accelerator/sample/order.go "$demo_dir/"
cp /home/runner/work/sodapop/sodapop/docs/accelerator/sample/order_test.go "$demo_dir/"
cp /home/runner/work/sodapop/sodapop/docs/accelerator/sample/README.md "$demo_dir/"
cd "$demo_dir"
go mod init example.com/sodapop-orders
go test ./...
git init
git add go.mod order.go order_test.go README.md
git -c user.name="Demo Operator" -c user.email="demo@example.invalid" \
    -c core.hooksPath=/dev/null -c commit.gpgsign=false commit -m "Synthetic exercise baseline"
git status --short
sodapop
```

Git identity settings above apply only to that single explicit operator command.
Do not copy `.git`, credentials, environment files, runtime state, or customer
code into the workspace. The short status should be empty before launching.
Go initialization uses the standard tool; no dependencies or network services
are needed for the sample tests.

Follow the [presenter guide](../demo.md) for sign-in, prompts, approvals,
validation, failure handling, and handoff. Keep Autopilot off. Planning is
advisory; deny unexpected edits or commands. Ordinary approved shell commands
are not sandboxed.

## Recreate and clean up safely

Exit Sodapop before starting another run. Create a **new** temporary directory
using the steps above; never use `git reset`, `git clean`, or checkout commands
against the user's worktree. Cancellation does not roll back a previous run.
Resuming an old session is not a fresh exercise; a new canonical directory
establishes a new project boundary.

After reviewing any evidence you want to keep, verify the exact disposable
directory path and remove only that directory using your file manager. Do not
delete application state, keyring credentials, or unrelated temporary folders.
Use `/logout` explicitly if sign-out is intended; uninstalling the executable
does not remove credentials or history.
