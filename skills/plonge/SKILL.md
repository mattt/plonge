---
name: plonge
description: Find and clean developer caches, downloaded dependencies, and build output. Use when disk space is low, a build fails with "no space left on device", or the user asks to clean caches, node_modules, virtual environments, or tool downloads.
---

# Plonge

Plonge reports project caches and global caches outside projects.
A cache is a directory or set of files selected by a cleanup rule.
Project roots come from mise's record of loaded config files,
or from CLI `--root` options.
`scan` and `clean` without `--apply` delete nothing.

If the Plonge MCP server is available, use its `scan`, `clean`, and `disk` tools.
MCP parameters have the same meaning as the corresponding CLI options,
but MCP does not support `--root` or `--no-mise`.

## Steps

1. Check free space with `plonge disk`.
   It reports space on the home directory's volume on macOS and Linux.

2. Scan before cleanup.
   Include global caches for a report of both project and global caches.

   ```console
   plonge scan --global
   ```

3. Show the largest caches, their sizes, and the totals.
   A quiet project has known activity at least `quiet_hours` old
   (24 hours by default).
   An active project has more recent or unknown activity.
   A busy project has a running or initializing Paseo agent.
   Project cleanup excludes busy projects and, by default, active projects.
   Say which global rules are `prune` and which are `clear`.
   A `prune` rule uses the tool's policy to select unused or stale data.
   A `clear` rule can remove cached data still in use;
   later commands may need to download dependencies or rebuild files.
   Global cleanup does not use the project activity or Git checks.

4. Run a dry run and show the output.
   Project and global cleanup are separate operations:

   ```console
   plonge clean
   plonge clean --global
   ```

5. Get the user's approval of the dry run before `--apply`.
   State the selected paths or actions and the estimated space to recover.
   Do not promise that the whole reported size will become free space.
   Keep the approved selection options when you apply the cleanup.

   ```console
   plonge clean --apply
   plonge clean --global --apply
   ```

6. Report the estimated space freed and any skipped or failed cleanup.
   Run `plonge disk` again to check free space on the home directory's volume.

## Selection options

Get approval for these options as part of the dry run:

- `--include-active` selects active projects, but still excludes busy projects.
- `--include-clear` with `--global` selects clear rules.
- `--min-size 100MiB` skips project caches smaller than 100 MiB.

`--global` cannot be combined with `--include-active`, `--min-size`,
`--root`, or `--no-mise`.

Project activity is based on Git HEAD timestamps, Claude transcripts,
and Paseo status, not on uncommitted edits or running builds.
If Paseo is missing or fails, its busy check provides no protection.
Do not clean caches in a project that you know is busy.

Do not use another deletion command to bypass a skipped path or rule.
Check the reported reason and explain it to the user.
