# DOG-06: usage pause after recorded reset

## Finding

The available code and tests do not reproduce a launch pause after the locally
recorded deadline. The pause is a process-local absolute deadline set to
`time.Now() + config.UsageLimitCooldown` (currently six hours). Launches are
blocked only while `now.Before(codexPausedUntil)`; at the exact deadline they
are eligible. A usage-limited finished session is cleared by the scheduler once
that same predicate is false. The targeted test passes.

The reported Microboss incident cannot be fully correlated from the available
log artifact. The DOG-44 backlog description records a Dev Agent 2 `WaitDelay`
error at **2026-11-02 23:04:06 +02:00** and a pause-until display of
**2026-11-03 05:04:06 +02:00**. These are six hours apart. However, that
description is embedded in captured command output in
`workspaces/logs/orchestrator-20261102-201057.869560405.log`; the artifact does
not yield the original pause/skip event records as structured events. The
reported reset time, suspension interval, log gap, later network failure, and
the timezone/source of the displayed reset are not independently present as
verifiable event timestamps. Current host time during inspection was
2026-11-03 05:07:09 EET (+02:00), which is not evidence of the incident
machine's clock.

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
  launching. A newly recorded usage-limit error during that interval could
  extend the deadline; the captured evidence does not establish such a second
  event.
- Existing tests cover active pause, expired pause, resume after expiry, and
  resume with no pause (`scheduler_test.go:241-305`). They use synthetic times;
  there is no deterministic test reproducing a post-deadline skip or a
  suspension/network sequence.

## Assessment

There is no evidence here for a clock/monotonic-time defect. The quoted
pause-until time is the locally configured six-hour cooldown, not a provider
reset time. The timestamps as reported place the deadline at 05:04:06 +02:00;
to establish that a later skip violated it would require the timestamp and
offset of that skip, plus proof no later usage-limit event reset the deadline.
The log gap and suspension make those transitions unobservable, while the
subsequent network failure alone would not establish another pause.

No production change is warranted by this evidence. If the incident needs
resolution, a separate read-only log correlation should provide the original
pause and skip event lines (with timezone), process start/restart times, any
additional usage-limit classification between them, and the exact provider
reset display/source. If a skip is then shown after the latest recorded
deadline without a newer pause, create a focused deterministic regression
case before proposing a clock or state fix.

## Verification

`go test ./internal/orchestrator -run 'Test.*(Usage|Pause|Cooldown)' -count=1`
passed (`ok github.com/de-angelov/slopboss/internal/orchestrator`). No isolated
incident reproduction was possible because the original pause/skip records and
the reset timestamp are unavailable in the supplied artifacts.
