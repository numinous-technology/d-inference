# Numinous Forge on Darkbloom

Forge takes an issue, a requested change, or a pull request through isolated execution and verification. This fork shows the resulting code and the evidence behind it. [Upstream Darkbloom](https://github.com/Layr-Labs/d-inference) and its production systems are unchanged.

| Work | Result to inspect |
|---|---|
| Maintain: repair reconnect reputation | [PR #4](https://github.com/numinous-technology/d-inference/pull/4), [regression proof](evidence/issue-regression-proof.json), [task history](evidence/issue-task.json) |
| Repair CI findings: remove streaming lock dependency | [PR #5](https://github.com/numinous-technology/d-inference/pull/5), [regression proof](evidence/streaming-regression-proof.json), [task history](evidence/streaming-task.json) |
| Build: add protocol fuzz coverage | [PR #2](https://github.com/numinous-technology/d-inference/pull/2), [task history](evidence/prompted-task.json) |
| Review: reject broken code | [Closed PR #1](https://github.com/numinous-technology/d-inference/pull/1), [failing CI evidence](evidence/rejected-ci-task.json) |
| Stabilize verification: repair asynchronous test fixtures | [PR #6](https://github.com/numinous-technology/d-inference/pull/6), [before/after proof](evidence/fixture-regression-proof.json), [PR CI](evidence/fixture-ci-task.json) |
| Repeat: run a scheduled check | [Verified occurrence](evidence/scheduled-task.json), [both policy-bound occurrences](evidence/schedule-promotion.json); schedule paused after verification |

```mermaid
flowchart TB
    People[Maintainers and contributors] --> Request[Issue or requested improvement]
    People --> PR[Pull request]
    Clock[Configured schedule] --> Checks
    Request --> Work[Implementation worker]
    Work --> Checks[Fresh verification workers]
    PR --> Checks
    Checks --> Evidence[Tests, patch, source identity and task history]
    Evidence --> Review[Maintainer review]
    Review --> Feedback[Recorded feedback and revision]
    Feedback --> Work
    Review --> Merge[Merge and release decisions]
    Review --> Policy[Reviewed skills and check updates]
    Policy --> Work
    Policy --> Checks
```

Maintainers choose scope, resolve behavior questions, and review merges and releases. Contributors bring issues, improvements, and PRs. Forge implements scoped work, runs accepted checks, records attempts, and cleans up workers. Agent output is a candidate until separate verification passes.

The repair demonstrates review as part of the process: the first candidate passed its initial tests but review found a history-selection flaw. Recorded feedback led to a revised candidate and additional tests against memory and real PostgreSQL. Those lessons become versioned checks and skills for future tasks; existing tasks retain their original policy.

CI also exposed a separate pre-existing streaming lock dependency. We retained the failed CI result, reproduced the lock ordering, and submitted a focused repair through the prompted-work lane. When the fork advanced during that task, an explicit continuation carried its reviewed patch and feedback onto the new source; [the superseded task](evidence/streaming-superseded-task.json) remains recorded. A later implementation timed out before exporting its candidate; [the recovery record](evidence/streaming-recovery.json) identifies the successful edits reconstructed from its retained transcript and the new task that verifies them. The platform now preserves unfinished patches on agent deadlines, with a deployed timeout regression in its qualification. CI does not automatically rewrite arbitrary failed PRs; an operator scopes the repair.

## Review the checks and agent instructions

The [accepted policy snapshot](policy/darkbloom.json) lists profiles and required checks. [Engineering instructions](policy/skills/engineering.md) and [reputation instructions](policy/skills/reputation.md) carry reviewed lessons into future tasks. Their [manifest](policy/manifest.json) binds the files to the qualified deployment. Changes can be proposed in this fork; Numinous reviews and deploys them before they affect future work. A candidate PR cannot replace its own accepted checks.

## Inspect the evidence

[Repair PR CI](evidence/issue-ci-task.json) and [prompted PR CI](evidence/prompted-ci-task.json) record verification of each PR's merge with the target branch.

With operator access to the installed CLI:

```sh
numinous-forge task list
numinous-forge task get 66257843079c685ec7a310c33395c780
numinous-forge task export 66257843079c685ec7a310c33395c780 ./repair-evidence
numinous-forge schedule runs qualification-protocol
```

Public receipts omit credentials and private infrastructure details. Operator exports contain the complete input, review notes, attempts, logs, and artifact manifests.

## Demonstrated work

The examples cover coordinator and protocol improvements in this fork. Each linked result records the checks that ran and the revision they verified. Maintainers review the proposed behavior and decide what to merge.

## Follow work on a pull request

The Numinous Forge verification comment updates when checks are queued, running,
or complete. It identifies the revision and links to the workflow and results.
New commits trigger verification again and update the same comment.

An engineering task attached to the PR has its own progress comment so you can
distinguish an agent preparing a change from the independent checks on the PR. The task comment
shows reproduction, implementation, verification, and review readiness.
