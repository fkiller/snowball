# AGENTS.md

## Purpose

This repository is primarily developed using OpenAI Codex.

When Codex's available 5-hour usage approaches exhaustion, work must be handed off cleanly to Google Antigravity so that another coding agent can continue from the repository state without requiring access to the Codex conversation history.

The repository, Git history, tests, and `HANDOFF.md` are the source of truth.

---

# General Working Rules

## Repository First

Always inspect the existing repository before making architectural assumptions.

Prefer:

1. existing project conventions,
2. existing architecture,
3. existing dependencies,
4. existing tests,

over introducing new patterns or frameworks.

Do not perform broad refactors unless they are necessary for the current task.

Do not change public APIs, schemas, protocols, or externally visible behavior without a clear requirement.

---

# Codex Usage Guard

Codex usage must be actively protected so that enough capacity remains to create a reliable handoff.

The primary quota is the **5-hour Codex usage window**.

The weekly quota is secondary.

## Usage Check Command

Use CodexBar to inspect the current quota:

```bash
codexbar --format json
```

If needed, inspect only the Codex provider data from the returned JSON.

Do not estimate quota from conversation length or token counts.

Use the actual quota information returned by CodexBar whenever available.

---

# When to Check Usage

Check Codex usage:

1. At the beginning of a new working session.
2. Before starting a substantial implementation task.
3. Before starting a large refactor.
4. Before starting a long debugging or exploratory investigation.
5. After completing a significant implementation milestone.
6. Periodically during long autonomous runs.
7. Whenever there is reason to believe the current 5-hour window is approaching exhaustion.

Do not waste excessive calls checking quota after every trivial edit.

The goal is to detect the threshold before beginning another expensive unit of work.

---

# Quota Threshold Policy

## More than 15% remaining

Operate normally.

Continue implementation, debugging, testing, and reasonable exploration.

---

## 10%–15% remaining — PREPARE HANDOFF

Do not begin a large new work unit.

Prefer completing the current atomic task.

Begin updating `HANDOFF.md` while continuing only work that can reasonably be completed before the mandatory handoff threshold.

Avoid:

- large refactors,
- broad exploratory work,
- optional cleanup,
- speculative changes,
- unrelated fixes,
- starting another feature.

Preserve enough Codex capacity to produce a high-quality handoff.

---

## 10% or less remaining — MANDATORY HANDOFF

Immediately enter HANDOFF MODE.

Do not start new implementation work.

Do not attempt another feature.

Do not consume remaining quota on optional improvements.

Only perform work necessary to leave the repository in a safe and understandable state.

---

# HANDOFF MODE

When the 5-hour quota reaches **10% remaining or less**, perform the following sequence.

## 1. Stop Scope Expansion

Freeze the task scope.

Do not start additional features, refactors, investigations, or cleanup.

If an operation is currently partially completed, finish only the smallest safe atomic unit needed to avoid leaving obviously corrupted or misleading code.

---

## 2. Inspect Repository State

Run appropriate repository inspection commands, including where applicable:

```bash
git status
git diff
git diff --stat
git log -10 --oneline
```

Understand exactly what has changed before writing the handoff.

---

## 3. Validate Current Work

Run the most relevant tests that can reasonably be completed.

Examples may include:

```bash
npm test
npm run lint
pytest
cargo test
go test ./...
dotnet test
```

Use the project's actual commands.

Do not launch an expensive unrelated test suite merely for completeness if it threatens the handoff budget.

Record exactly what was and was not tested.

---

## 4. Preserve Work

Commit completed and internally consistent work when appropriate.

Do not commit knowingly broken code merely to create a checkpoint.

If useful uncommitted work must remain, preserve it and document exactly:

- which files contain it,
- what state it is in,
- why it was not committed,
- what the next agent should do with it.

Never discard useful work during handoff unless explicitly instructed by the user.

---

## 5. Update `HANDOFF.md`

Rewrite or update `HANDOFF.md` so that another coding agent can continue without access to the current Codex conversation.

The handoff must contain all materially relevant context.

Include:

### Current Objective

What the user is ultimately trying to accomplish.

### Current Task

The specific task currently being implemented.

### Current State

What currently works and what does not.

### Completed Work

Concrete work already completed.

Include relevant files and commits.

### Remaining Work

What remains unfinished.

Order this by recommended execution sequence.

### Exact Next Action

Give the next agent a concrete first action.

This should be specific enough that the next agent does not need to rediscover the state of the project.

### Architecture and Decisions

Document important decisions already made and why they were made.

Do not force the next agent to reverse-engineer architectural intent from code.

### Files Changed

List important modified or created files and their purpose.

### Tests and Verification

Record:

- tests executed,
- results,
- tests not executed,
- known failures.

### Known Problems

Record unresolved bugs, uncertain behavior, edge cases, or incomplete implementations.

### Failed Approaches

Record important attempts that failed so the next agent does not unnecessarily repeat them.

### Constraints

Record requirements or things that must not be changed.

### Commands

Include useful commands for running, building, testing, or reproducing the current state.

### Git State

Record:

- current branch,
- latest relevant commit,
- uncommitted changes,
- stash state if relevant.

### Recommended Next Steps

Provide a short ordered sequence such as:

1. First action
2. Second action
3. Verification step
4. Follow-up implementation

---

# Handoff Quality Rule

A successful handoff means that a capable coding agent can:

1. clone or open the repository,
2. read `AGENTS.md`,
3. read `HANDOFF.md`,
4. inspect Git history,
5. continue useful work immediately,

without needing the previous Codex conversation.

If important knowledge exists only in the conversation, move it into repository documentation before stopping.

---

# Handoff Completion

After `HANDOFF.md` has been updated:

1. inspect it for missing context,
2. verify repository state one final time,
3. commit the handoff file if appropriate,
4. stop implementation work.

Report to the user that Codex reached the configured quota guard and that the repository is ready for Antigravity takeover.

Do not continue coding after mandatory handoff merely because some quota remains.

The final 10% is reserved for safe shutdown and state transfer.

---

# Quota Tool Failure

If:

```bash
codexbar --format json
```

cannot obtain valid quota information:

1. do not invent a percentage,
2. do not assume quota is healthy,
3. briefly diagnose obvious local issues,
4. continue normal work if no evidence indicates quota exhaustion,
5. tell the user that automatic quota protection is temporarily unavailable if the failure persists.

Do not spend substantial project time repairing CodexBar unless explicitly requested.

---

# Agent Identity

When writing commits or documentation, avoid making implementation behavior depend on a specific AI provider.

Code should remain maintainable by humans and other coding agents.

Agent-specific information belongs in `HANDOFF.md` or development metadata, not in application behavior.

---

# Source of Truth Priority

When information conflicts, use this order:

1. Explicit current user instruction
2. Current repository state
3. Current tests and observable behavior
4. `AGENTS.md`
5. `HANDOFF.md`
6. Git history
7. Previous conversational assumptions

Never preserve an outdated handoff instruction over newer repository evidence.

## Cross-Agent Handoff

This repository may be worked on by different coding agents.

If `HANDOFF.md` exists and its handoff state is `READY`, treat the repository as work handed off from another agent.

Before making new implementation changes:

1. Read `HANDOFF.md` completely.
2. Run:
   - `git status`
   - `git log -10 --oneline`
   - `git diff`
3. Verify that the handoff description matches the actual repository state.
4. Continue from the documented `Exact Next Action`.
5. Do not redo completed work unless repository evidence shows it is incomplete or incorrect.
6. Preserve existing architectural decisions unless there is a concrete reason to change them.
7. Keep `HANDOFF.md` updated when project state materially changes.

Repository state, tests, and Git history take precedence over stale handoff notes.

Do not assume access to another agent's previous conversation.

# Snowball repository rules

These rules apply to every change in this repository.

## Product boundary

- Snowball is LAN-only. Do not add a wildcard bind, inbound WAN listener, public deployment, STUN/TURN service, or cloud relay without explicit approval and a threat-model update.
- Preserve the `snowball-voice` container, volume, daemon, and state paths unless a tested migration is part of the change. The persistent Chromium session and credentials depend on them.
- Never commit `/data`, browser profiles, certificates, setup codes, passwords, session cookies, VAPID private keys, or generated runtime state.
- Do not restart or replace the production router container unless the user explicitly requests deployment. Build and test under a separate image tag and temporary state volume.

## Authentication and API safety

- Every API route is deny-by-default. Only health and authentication bootstrap/login/status routes may be public.
- Every authenticated state-changing route must use `g.protect(handler, true)`. Read-only sensitive routes use `g.protect(handler, false)`.
- Browser writes must go through the authenticated request helper so the CSRF header is present. Do not expose the session cookie to JavaScript.
- Keep the Browser Console behind nginx `auth_request`. ChatGPT authentication and Snowball administrator authentication are separate trust boundaries.
- Accept JSON only with strict unknown-field rejection and a bounded request body. Validate and normalize values before persistence or browser automation.
- Write sensitive state atomically with mode `0600`. Do not log passwords, cookies, CSRF tokens, or browser credentials. The one-time bootstrap code is the only setup secret allowed in logs and must be deleted after setup.

## Voice and browser automation

- Treat the browser controller as an unstable adapter. Prefer explicit capability errors over clicking a broad or ambiguous selector.
- The Gateway/browser status is authoritative for ChatGPT Voice state. A WebRTC connection alone must never display “Voice is live.”
- Opening Browser Console or authenticating must never start Voice.
- Keep user-facing prompt text and command aliases in the versioned settings resources, not scattered through automation code.
- Explicit `Codex Project` commands take Codex precedence; plain `Project` commands target ChatGPT even when names collide.

## Required verification

Run these checks for every relevant change:

```bash
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
cd gateway && go test -race ./... && go vet ./...
docker build --network host -t snowball-voice:test .
```

Security-sensitive changes also require authentication, CSRF, origin, settings-validation, and nginx console-gate tests. Do not bypass a failing gate to publish an image.
