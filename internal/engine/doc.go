// Package engine connects Sodapop to its bundled Copilot runtime. New validates
// account/project/storage identity but starts no runtime; Start requires an
// explicit Sodapop token and an available runtimebundle.Path.
//
// Send returns when the SDK acknowledges delivery, not when the turn completes.
// EventIdle ends a turn. A failed send is never replayed and may require Abort
// before another send. Abort cancels pending decisions, waits for runtime idle
// and callback completion, then emits a final cancellation EventIdle with
// Name="aborted" without reverting files. An already-idle abort also emits that barrier.
// The UI should process both the Abort result and this idle barrier before
// accepting another turn, since pre-abort events may already be in its inbox.
// Close is idempotent and returns owned-process cleanup errors.
//
// Session changes preserve SDK history. Resume only accepts this account and
// canonical project's index records; history arrives as final EventMessage
// records with History=true and Role=user, assistant, or tool. Consumers should
// buffer events while create/resume is in flight and reconcile live assistant
// finals by MessageID. Event.ID identifies the source event, not the message;
// history replay preserves source IDs. Permission IDs are runtime request IDs;
// legacy SDK questions lack request IDs, so their IDs are local to an attachment.
// Respond/Cancel resolve once without waiting for network I/O.
//
// TokenSource must remain bound to AccountID until this engine is closed. Tokens
// are supplied explicitly on create/resume and refreshed before Send/SetModel.
// A mutable account manager's bare Token method does not provide that binding:
// the host must serialize account-changing Login/Logout with token-consuming
// operations, close/drain the old engine, and recreate it for the new account.
// Separate Current and Token calls do not form an atomic account/token pair.
// The frozen TokenSource contract has no expiry, so the SDK's ExpiresIn-required
// token provider cannot be configured honestly. Mid-turn expiry and client-level
// auth renewal require an explicit reconnect/resume, never an automatic replay.
// No live OAuth or runtime/model compatibility qualification is implied by this
// adapter's deterministic tests.
package engine
