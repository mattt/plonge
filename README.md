# Plonge

Developer tools and coding agents fill a disk with caches.
Each project gets its own `node_modules`, `.build`, `DerivedData`, `target`,
or virtual environment, and each agent worktree gets another copy.
Package managers keep shared caches elsewhere.

When the disk is full, you need space now,
and that is a bad time to delete folders by hand.
A folder that looks disposable can hold work that exists nowhere else,
such as a dependency checkout with commits that were never pushed.
Checking each folder first takes time, so the checks get skipped.

Plonge does those checks for you.
It finds the caches, reports their sizes,
and deletes nothing until you add `--apply`.
In projects, it deletes only directories that match its rules and pass its Git checks.
For shared caches, it runs each tool's cleanup command or a specific built-in rule.
See [What Plonge covers](docs/coverage.md) for supported tools and known gaps.

## Install

With Homebrew:

```console
brew install mattt/tap/plonge
```

With mise, through its `github` or `go` backend:

```console
mise use -g github:mattt/plonge
mise use -g go:github.com/mattt/plonge/cmd/plonge@latest
```

With Go 1.26 or later:

```console
go install github.com/mattt/plonge/cmd/plonge@latest
```

The [releases page](https://github.com/mattt/plonge/releases)
also has archives for macOS and Linux, on amd64 and arm64.

Project scans need Git on `PATH`.
Global cleanup needs the tool named by each rule.
The `disk` command supports macOS and Linux.

## Try a safe run

```console
plonge scan
plonge clean
```

Both commands delete nothing.
`scan` reports cache sizes, projects, last activity, and matched rules.
`clean` shows which caches qualify for deletion.
Add `--json` to either command for structured output.

Plonge gets project directories from mise's record of loaded config files.
To scan only the project in your current directory:

```console
plonge scan --no-mise --root .
plonge clean --no-mise --root .
```

A root is a directory to scan; the report calls each root a project.
`--root` adds a root and can be repeated.
`--no-mise` limits the scan to the roots you give.
A root must be inside a Git work tree.
Plonge skips a root outside one,
and does not search a parent folder for repositories.

A cache is a directory that matches a rule, such as `node_modules` or `.venv`.
Some rules also require a project file:
`target`, for example, must be beside `Cargo.toml` or `pom.xml`.
A match must pass the Git and safety checks below before it enters the totals.

## Delete project caches

Review the dry run, then add `--apply` with the same selection options:

```console
plonge clean --no-mise --root .
plonge clean --no-mise --root . --apply
```

By default, only caches in quiet projects qualify.
A quiet project has a known last activity time at least 24 hours old;
`quiet_hours` in your rules file changes that period.
Activity is the newest time in the root's Git HEAD log
and its Claude Code transcripts.
Uncommitted edits and builds do not update it,
so a quiet status does not prove that a project is unused.
A project with more recent or unknown activity is active.
`--include-active` also selects active projects,
and `--min-size 100MiB` skips caches smaller than 100 MiB.
Projects that the agent status check reports as busy stay excluded,
even with `--include-active`, and a failed status check blocks project cleanup.

Before each deletion, Plonge checks that the path is still a directory,
is not a symlink, is inside its root, is ignored by Git,
and contains no tracked files.
It also refuses a directory that holds an entry that its rule protects;
the Mix `_build` rule, for example, protects releases and dependency sources.
Plonge skips a path that does not pass, or reports an error.
Cleanup deletes directories and does not run project code.
It continues after a deletion error and exits with a nonzero status.

After `--apply`, the report gives an estimate of the space freed, from cache sizes,
and the measured change in free space on your home directory's volume.
Other processes can also change that second number.

An ignored cache can contain a dependency cloned from Git,
with commits or edits that the enclosing repository cannot see.
Plonge therefore refuses a cache that holds a Git repository at any depth,
or a directory that it cannot read.
The report lists each refused cache and its reason, outside the totals:

```console
Refused ~/code/app/node_modules: contains a Git repository at some-dep
```

The one exception is Swift Package Manager.
In a `.build` or `DerivedData` directory beside `Package.swift`,
Plonge allows dependency clones that are unchanged
and that SwiftPM can make again from `Package.resolved`.
It refuses the directory when a clone has edits, extra commits, or any other repository.
The rule reference describes the
[Git check](docs/rules.md#caches-that-hold-a-git-repository), the
[SwiftPM conditions](docs/rules.md#swift-package-manager-checkouts), and
[project discovery and activity](docs/rules.md#project-discovery-and-activity).

## Global caches

Shared package-manager caches live outside projects.
To inspect them and preview their cleanup actions:

```console
plonge scan --global
plonge clean --global
```

`scan --global` adds global caches to the project report.
`clean --global` selects only global actions,
so it cannot be combined with `--root`, `--no-mise`, `--include-active`, or `--min-size`.
Both run each tool's lookup and check commands, even in a dry run.

Global rules have two kinds.
A `prune` rule uses the tool's policy to select unused or stale data,
as `mise cache prune` and `uv cache prune` do.
A `clear` rule can remove cached data that is still in use,
so later commands may have to download dependencies or rebuild files.
Clear rules need `--include-clear`.
Most prune rules report a size marked `(whole cache)`,
of which the tool removes only part.

After you review the dry run, apply it with the same flags:

```console
plonge clean --global --apply
plonge clean --global --include-clear
plonge clean --global --include-clear --apply
```

Global cleanup does not use project activity,
and it can interrupt a build that is using a selected cache.
Plonge runs each tool from your home directory, without a shell,
so project-specific tool settings may not apply.
It uses `mise exec --` when mise provides the tool, and `PATH` otherwise,
and it skips and reports a tool that is missing.
The `mise-tools` rule skips cleanup when mise cannot load a tracked config
that may still exist, because mise could remove a version that the config pins.

Where a tool has no safe cleanup command,
a built-in rule deletes the contents of one specific directory.
Those rules are clear rules.
They refuse unsafe paths, such as the home directory, a symlink,
or a path in or containing a Git repository,
and they protect data that a download cannot restore.
The Cargo rule leaves Git checkouts alone,
the Deno rules leave localStorage and REPL history alone,
and the Maven rule refuses a repository with locally installed artifacts.
On macOS, a prune rule for Xcode selects only build directories
whose workspace no longer exists.

See the [rule reference](docs/rules.md#global-caches) for every global action,
its path, and its checks.
The [coverage table](docs/coverage.md) explains tool-specific limits.

## Add a rule

Write custom rules to `$XDG_CONFIG_HOME/plonge/rules.toml`,
or `~/.config/plonge/rules.toml` by default.
For example, to add a cache directory used by your build tool:

```toml
names = [".build-cache"]
quiet_hours = 72

[beside]
".build-cache" = ["build.toml"]
```

This matches `.build-cache` only beside `build.toml`
and makes the quiet period 72 hours.
The directory must still pass every project safety check.
Choose only directories that your tools can restore:
Git cannot protect edits inside an ignored folder
that has no repository of its own.
Use `refuse_inside` to protect files within a cache that a rebuild cannot restore.

`names` and `skip` add to the defaults.
A `beside` or `refuse_inside` entry replaces the default for that name,
and a global rule replaces the whole default rule with the same `id`, including its checks.
See the [rule reference](docs/rules.md) for the full format
and the [default rules](internal/rules/default.toml) before you replace an entry.

## Disk space

Reported cache sizes estimate the space that cleanup could free.
On macOS, Plonge counts only the blocks that a cache does not share
with files outside it, so a size can be smaller than what `du` reports.
Files linked elsewhere, snapshots, and concurrent changes
can change the space freed, and global size totals can overlap.
The rule reference explains [how sizes are measured](docs/rules.md#sizes-and-freed-space).

`plonge disk` prints the free space on your home directory's volume:

```console
plonge disk
plonge disk --warn-below 20GiB
```

With `--warn-below`, Plonge prints a warning only when free space is below that size.
Size and disk errors then go to standard error with exit status 0,
so the command is safe in a hook.
Sizes accept `KiB`, `MiB`, `GiB`, and `TiB` (or `K`, `M`, `G`, and `T`) as powers of 1024,
and `KB`, `MB`, `GB`, and `TB` as powers of 1000.

## MCP server

`plonge mcp` serves Plonge to coding agents over standard input and output
(stdio), with three tools:

- `scan` reports caches; `global: true` adds the global caches.
- `clean` is a dry run unless `apply: true`.
  It takes `include_active`, `min_size`, `global`, and `include_clear`,
  with the same rules as the command line.
- `disk` reports free space.

The tools use mise project discovery and do not accept `--root` or `--no-mise`.
Their descriptions tell agents to show the dry run
and get your approval before `apply`.
The server does not enforce that approval.

To add the server to Claude Code:

```console
claude mcp add plonge -- plonge mcp
```

## Agent skill

`skills/plonge/SKILL.md` tells coding agents when to use Plonge,
to scan first, to show sizes, and to ask you before `clean --apply`.
To install it for Claude Code, copy `skills/plonge` to `~/.claude/skills/plonge`.

## Hooks

This Claude Code `SessionStart` hook shows the agent a warning when free space is low.
Add it to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "plonge disk --warn-below 20GiB"
          }
        ]
      }
    ]
  }
}
```
## Development

The repository pins its tools in `mise.toml`.

```console
mise install
mise exec -- make check
```

`make build` writes `bin/plonge`.
`make lint` runs `gofmt -l` and `go vet`.
`make test` runs the tests with gotestsum.

## License

This project is available under the MIT license.
See the LICENSE file for more info.
