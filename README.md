# Looptab

Looptab is a readable schedule file for Codex loops.

Run `looptab` to edit `~/.config/looptab/looptab`, then let the scheduler invoke Codex from the working directories you name.

## Install

```sh
go install github.com/ryangerardwilson/looptab/cmd/looptab@latest
looptab help
```

Looptab expects the Codex CLI to be available as `codex` on `PATH`. If Codex lives somewhere else, set `CODEX_BIN`.

## File Format

The first non-comment line may set the timezone. If it is omitted, Looptab uses `UTC`.

Each active job line has one readable schedule, an optional working directory, and one quoted prompt. When the directory is omitted, Looptab runs Codex from `~`.

```text
timezone UTC

now "Run once from home when looptab loads."
now ~/Work/example "Run once from this repo when looptab loads."
daily 11am "Review from home and fix one small obvious issue."
daily 11am ~/Work/example "Review the repo and fix one small obvious issue."
daily 11am,12pm,1pm /home/ryan/Apps/example "Run a quick maintenance pass."
weekdays 9am ~/Work/example "Plan the day and update TODOs."
weekends 5am ~/Work/example "Check quiet weekend maintenance."
mondays 5am ~/Work/example "Prepare the weekly review."
```

Supported schedules:

```text
now
daily <time[,time...]>
weekdays <time[,time...]>
weekends <time[,time...]>
monday|mondays <time[,time...]>
tuesday|tuesdays <time[,time...]>
wednesday|wednesdays <time[,time...]>
thursday|thursdays <time[,time...]>
friday|fridays <time[,time...]>
saturday|saturdays <time[,time...]>
sunday|sundays <time[,time...]>
```

`now` runs once each time `looptab run` loads the file. If you edit and save the file while the scheduler is running, `now` jobs in the new file run once after that reload.

Times may be written as `11am`, `9:30am`, `5pm`, `17:15`, or comma-separated lists such as `11am,12pm,1pm`.

When present, the working directory must be absolute or start with `~`. The prompt must be quoted.

## Commands

```text
looptab
  open ~/.config/looptab/looptab

looptab check
  validate the loop file, timezone, working directories, and Codex binary, then print parsed job IDs

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
