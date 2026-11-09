---
name: plonge
description: Finds and removes the caches that developer tools and coding agents leave on disk. Use when the disk is low on space, when a build fails with "no space left on device", or when the user asks to clean up caches, node_modules, virtual environments, or tool downloads.
---

# Plonge

Plonge reports caches in the projects that mise tracks,
and caches outside projects, such as the ones that uv, Go, and npm keep.
It deletes only when you give `--apply`.

If the `plonge` MCP server is available, use its `scan`, `clean`, and `disk` tools.
Their options have the same meaning as the command-line options below.

## Steps

1. Check the free space with `plonge disk`.

2. Scan first.
   Include global caches when the user wants the most space back.

   ```console
   plonge scan --global
   ```

3. Show the user the largest caches with their sizes,
   the total, and the total in quiet projects.
   Say which global rules are `prune` and which are `clear`.
   A `clear` rule removes everything, so the next build downloads it again.

4. Do a dry run, and show its output to the user.

   ```console
   plonge clean
   plonge clean --global
   ```

5. Ask the user before each `--apply`.
   State what it will delete and how much space it will free.
   Do not run `--apply` without the user's approval of that dry run.

   ```console
   plonge clean --apply
   plonge clean --global --apply
   ```

6. Report the space freed, and each cache that plonge skipped or failed to delete.

## Options that remove more

Use these only when the user asks, or after the user approves them:

- `--include-active` also deletes caches in projects with recent activity.
  The next build in those projects must rebuild them.
- `--include-clear` with `--global` also runs `clear` rules.
- `--min-size 100MiB` leaves out small caches, so it removes less.

Plonge never deletes in a project where a Paseo agent is running.
Do not delete those caches another way.

Do not delete a cache with `rm` to get past something that plonge skipped.
Plonge skips a path when it is no longer safe to delete, and it reports the reason.
