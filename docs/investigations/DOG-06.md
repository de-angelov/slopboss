# DOG-06: usage pause after recorded reset

## Finding

The available code and tests do not reproduce a launch pause after the locally
recorded deadline. The pause is a process-local absolute deadline set to
`time.Now() + config.UsageLimitCooldown` (currently six hours). Launches are
blocked only while `now.Before(codexPausedUntil)`; at the exact deadline they
are eligible. A usage-limited finished session is cleared by the scheduler once
that same predicate is false. The targeted test passes.

The available log artifact contains the actual pause/skip events. At
**2026-11-02 23:04:06 +02:00**, the Dev Agent 2 `WaitDelay` error was classified
as a usage limit and the orchestrator recorded a deadline of **2026-11-03
05:04:06 +02:00**, exactly six hours later. Repeated skips for all three roles
through **2026-11-02 23:05:54 +02:00** report that same deadline. The log then
records a *new* usage-limit pause at **2026-11-03 05:11:40 +02:00**, setting a
new deadline of **11:11:40 +02:00**. This is 7 minutes 34 seconds after the
first deadline and directly supports a new classified usage-limit event
restarting the cooldown after expiry. It does not show a skip against the old
deadline after that deadline. The pause-until timestamp is the orchestrator's
cooldown deadline, not independently verified provider reset metadata. The
reported suspension interval, log gap, and later network failure are not
independently timestamped in this artifact, so their relationship to the
05:11:40 event cannot be established.

## State and clock evidence

- `runSession` calls `pauseLaunchesAfterUsageLimit(time.Now())` only when the
  provider command returns an error and its monitor reports a usage limit
  (`internal/orchestrator/session.go:207-212`). An ordinary network error does
  not set the global usage pause through this branch.
- The pause deadline is extended only when the new deadline is later than the
  current one. It is process-local state protected by the scheduler mutex and
  is not persisted (`internal/orchestrator/scheduler.go:656-690`).
- The pause check compares `time.Now()` with the stored deadline. Go's
  `time.Now()` values can carry monotonic clock readings, and `Add` preserves
  them, so in-process comparisons can use monotonic time and resist wall-clock
  corrections. The log uses `Format(time.RFC3339)`, which shows wall time and
  numeric offset but not monotonic time. After process restart, this global
  deadline is not restored.
- Scheduler first clears a usage-limited `finished` entry when its deadline
  has elapsed, then independently checks the global pause immediately before
  launching. The captured log establishes a second pause at 05:11:40, after
  the first deadline; it does not provide enough context to connect that event
  to the reported network failure or suspension.
- Existing tests cover active pause, expired pause, resume after expiry, and
  resume with no pause (`scheduler_test.go:241-305`). They use synthetic times;
  there is no deterministic test reproducing a post-deadline skip or a
  suspension/network sequence.

## Assessment

There is no evidence here for a clock/monotonic-time defect or for a launch
skip after the first deadline while it remained the latest deadline. Instead,
the log records another usage-limit pause at 05:11:40 +02:00, after which skips
report the new 11:11:40 deadline. A network failure alone would not trigger
this branch; the second event means the provider monitor classified the failed
command as usage-limited. The underlying command result and monitor evidence
for that second classification are not available in the extracted incident
context, so the exact trigger remains unverified.

No production change is warranted by this evidence. To explain why the second
usage-limit classification occurred, a separate read-only correlation should
provide the 05:11:40 session's command result and provider monitor details,
process/suspension state, and the exact provider reset display/source. If a
skip is shown after the latest recorded deadline without a newer pause, create
a focused deterministic regression case before proposing a clock or state
fix.

## Verification

`go test ./internal/orchestrator -run 'Test.*(Usage|Pause|Cooldown)' -count=1`
passed (`ok github.com/de-angelov/slopboss/internal/orchestrator`). No isolated
incident reproduction was possible because the original pause/skip records and
the reset timestamp are unavailable in the supplied artifacts.
