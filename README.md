# Looptab

Looptab is crontab for Codex loops.

Run `looptab` to edit `~/.config/looptab/looptab`, then let the scheduler invoke Codex from the working directories you name.

## Install

```sh
go install github.com/ryangerardwilson/looptab/cmd/looptab@latest
looptab help
```

Looptab expects the Codex CLI to be available as `codex` on `PATH`. If Codex lives somewhere else, set `CODEX_BIN`.

## File Format

Each active line has one schedule, one working directory, and one quoted prompt:

```cron
# minute hour day month weekday cwd prompt
0 * * * * ~/Work/example "Review the repo and fix one small obvious issue."
30 9 * * 1-5 /home/ryan/Apps/example "Run tests, inspect failures, and make a minimal fix."
@daily ~/Work/notes "Summarize yesterday's notes and update TODOs."
```

Supported schedules are standard five-field cron expressions plus descriptors such as `@hourly`, `@daily`, and `@weekly`.

The working directory must be absolute or start with `~`. The prompt must be quoted.

## Commands

```text
looptab
  open ~/.config/looptab/looptab

looptab check
  validate the loop file, working directories, and Codex binary, then print parsed job IDs

looptab run
  run the scheduler in the foreground

looptab run job <id>
  run one parsed job immediately

looptab logs
  show a formatted history of all Codex loop runs

looptab logs job <id>
  show one job's run history and latest output tail

looptab service install
looptab service start
looptab service stop
looptab service status
looptab service remove
  manage the Linux systemd user service
```

## Logs

Looptab records every run in:

```text
~/.local/state/looptab/runs.jsonl
~/.local/state/looptab/logs/
```

`looptab logs` prints a summary table:

```text
Looptab runs
when                     status  duration  job       cwd                 report
2026-06-10 10:00:00 IST  ok      1m12s     a1b2c3d4  ~/Work/example      Updated README and ran tests.
2026-06-10 11:00:00 IST  failed  8s        a1b2c3d4  ~/Work/example      codex exited with status 1
```

The JSONL history is the audit trail. The per-run `.log` files contain full Codex output.

## Background Service

On Linux, install the systemd user service:

```sh
looptab service install
looptab service start
looptab service status
```

The service uses the installed `looptab` binary path and records runs under `~/.local/state/looptab/`.

## Safety

Looptab does not run arbitrary shell commands. It invokes Codex directly as:

```text
codex exec --color never --cd <cwd> <prompt>
```

Jobs for the same ID do not overlap. If the next scheduled time arrives while the previous run is still active, Looptab records a skipped run.

Missed times are not replayed after sleep or downtime.
