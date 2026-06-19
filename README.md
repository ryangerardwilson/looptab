# Looptab

Looptab is the source of truth for scheduled AI loops and direct commands.

Run `looptab` to edit `~/.config/looptab/looptab`, then let the scheduler invoke Codex, Grok, or a direct executable on the cadence you define.

## Install

```sh
go install github.com/ryangerardwilson/looptab/cmd/looptab@latest
looptab help
```

Looptab resolves executables lazily per job kind:

- Codex: `codex` on `PATH`, or `CODEX_BIN`
- Grok: `grok` on `PATH`, or `GROK_BIN`
- Command jobs: the executable path you name in the looptab file

## File Format

The first non-comment line may set the timezone. If it is omitted, Looptab uses `UTC`.

Each active job line has one readable schedule, an optional working directory, and one action. When the directory is omitted, Looptab runs from `~`.

```text
timezone UTC

now "Run once with Codex from home when looptab loads."
daily 5am @grok "Check my emails and prepare me a brief."
daily 11am @codex ~/Work/example "Review the repo and fix one small obvious issue."
daily 5am ~/.local/bin/gmail sync
hourly "Review from home once per hour."
hourly at 15 ~/Work/example "Review the repo at minute 15 every hour."
weekdays 9am ~/Work/example "Plan the day and update TODOs."
mondays 5am ~/Work/example "Prepare the weekly review."
```

Action forms:

```text
"<prompt>"                 # Codex (default)
@codex "<prompt>"          # Codex
@grok "<prompt>"           # Grok
<executable> [args...]     # direct command, no shell
```

For AI jobs, the working directory must appear before `@grok`, `@codex`, or the quoted prompt when you set one explicitly. For command jobs, a path is only treated as `cwd` when it is followed by `@grok`, `@codex`, or a quoted prompt.

Scheduled AI jobs run headlessly. Manual `looptab now` and `looptab now <index-or-job-id>` launch the Codex or Grok TUI when the first step is an AI job and stdin, stdout, and stderr are real terminals. Outcome branches and later chain steps run headlessly after the TUI exits, so exit-code follow-ups keep working.

Supported schedules:

```text
now
hourly
hourly at <minute>
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

`now` runs once per saved looptab file load. If you edit and save the file while the scheduler is running, `now` jobs in that saved file run once after reload.
The background scheduler records claimed `now` jobs by job ID and file modification time in `~/.local/state/looptab/now-runs.json`, so restarting the service does not rerun the same saved file. Use `looptab run now` to force a manual run.

Times may be written as `11am`, `9:30am`, `5pm`, `17:15`, or comma-separated lists such as `11am,12pm,1pm`.
`hourly` runs at minute `0` each hour. Use `hourly at 15` to run at minute `15` each hour.

When present, the working directory must be absolute or start with `~`. AI prompts must be quoted.

## Commands

```text
looptab
  open ~/.config/looptab/looptab, then start the background scheduler when jobs exist

looptab check
  validate the loop file, timezone, working directories, and required executables, then print parsed job IDs

looptab run
  run the scheduler in the foreground

looptab run now
  run every now job once immediately

looptab run job <id>
  run one parsed job immediately

looptab now
  pick a registered job and run it immediately; AI-first jobs open the agent TUI

looptab now <index-or-job-id>
  run a registered job immediately; AI-first jobs open the agent TUI

looptab inspect
  follow the only active run's live output

looptab inspect <job-or-run-id>
  follow active output or show the latest completed output for a job or run

looptab stream
  stream live output across all active loops

looptab stream <index>
  stream live output for one active loop by the index shown in `looptab status`

looptab kill <index>
  kill one active loop by the index shown in `looptab status`

looptab status
  show currently running loops with kill indexes

looptab status json
  print active loop state for bars and scripts

looptab status watch
  stream active loop state as JSON lines for desktop bars and scripts

looptab service install
looptab service start
looptab service stop
looptab service restart
looptab service status
looptab service remove
  manage the Linux systemd user service
```

## Internal Run State

Looptab keeps internal run state in:

```text
~/.local/state/looptab/runs.jsonl
~/.local/state/looptab/logs/
~/.local/state/looptab/active/
```

The JSONL history is the audit trail for Looptab itself. The per-run output files contain full job output and are used by `looptab inspect`, `looptab stream`, status integrations, and completed-run lookup. While a run is active, output is written to that run's output file as it arrives.

## Background Service

On Linux, `looptab` installs and starts the systemd user service after you edit and save a file with jobs.

You can still manage the service directly:

```sh
looptab service install
looptab service start
looptab service restart
looptab service status
```

The service uses the installed `looptab` binary path and records runs under `~/.local/state/looptab/`.

## Safety

Looptab does not run arbitrary shell pipelines. It invokes executables directly:

```text
codex exec --color never --cd <cwd> <prompt>
grok --always-approve --cwd <cwd> -p <prompt>
<executable> [args...] with cwd set to the job directory
```

Jobs for the same ID do not overlap. If the next scheduled time arrives while the previous run is still active, Looptab records a skipped run.

Missed times are not replayed after sleep or downtime.

## Periodic Job Policy

Use Looptab for routine automation that should be visible in one file. Prefer adding or moving jobs into `~/.config/looptab/looptab` instead of creating new app-owned systemd timers for the same work. When a job moves to Looptab, disable the redundant timer so only one scheduler owns that cadence.
