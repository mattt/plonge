# Plonge

Developer tools and coding agents leave large caches on disk.
A project can have `node_modules`, `.build`, `DerivedData`, `target`,
or a virtual environment, while package managers keep shared caches elsewhere.
Across projects and agent worktrees, these folders keep growing.

When the disk fills up, deleting folders by hand is risky.
A folder that looks disposable can hold real work,
including a worktree's only copy of its data.
Much of the space can be reclaimed,
but checking each folder for files that cannot be restored takes time.
Those checks are easy to skip when you need space now.

Plonge finds these caches and reports them before it deletes anything.
For project caches, it deletes only what its rules and Git checks allow.
For shared caches, it uses tool cleanup commands or specific built-in rules.
See [What Plonge covers](docs/coverage.md) for supported tools and known gaps.

## Install

With Homebrew:

```console
brew install mattt/tap/plonge
```

With mise, through its `github` backend:

```console
mise use -g github:mattt/plonge
```

Or download the archive for your platform from the
[releases page](https://github.com/mattt/plonge/releases)
and put the `plonge` binary on your `PATH`.
Archives are available for macOS and Linux, on amd64 and arm64.

With mise, through its `go` backend:

```console
mise use -g go:github.com/mattt/plonge/cmd/plonge@latest
```

With Go 1.26 or later:

```console
go install github.com/mattt/plonge/cmd/plonge@latest
```

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
To scan just the project in your current directory:

```console
plonge scan --no-mise --root .
plonge clean --no-mise --root .
```

A root is a directory to scan; the report calls each root a project.
Use `--root` to add one, or `--no-mise` to use only the roots you give.
You can repeat `--root`; `--no-mise` requires at least one.
A root must be inside a Git work tree.
Plonge skips roots outside one, but still scans separately listed roots inside them.
It does not search a parent folder for repositories.

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
A project with more recent or unknown activity is active.
`--include-active` also selects active projects,
and `--min-size 100MiB` skips caches smaller than 100 MiB.
Projects reported as busy by the agent status check remain excluded,
even with `--include-active`, and a failed status check blocks project cleanup.

Before each deletion, Plonge checks that the path is still a directory,
is not a symlink, is inside its root, is ignored by Git,
and contains no tracked files.
It also refuses directories that are or hold Git repositories,
or hold entries that their rules protect.
For example, the Mix `_build` rule protects releases and dependency sources.

If a path check does not pass, Plonge skips the path or reports an error.
It can make read-only files and directories writable to remove them.
It continues after deletion errors and exits with a nonzero status.
Project cleanup deletes matching directories; it does not run project code.

### Caches that hold a Git repository

An ignored cache can contain a dependency cloned from Git,
with commits or edits that the enclosing repository cannot see.
Plonge refuses a cache with a `.git` entry of any type at any depth,
or a Git directory (`HEAD` beside `objects` and `refs`), such as a bare clone.
It also refuses a cache with a directory it cannot read,
because that directory could hide a repository.
It checks while measuring the cache and again before deletion,
without following symlinks.

The report lists each refused cache and its reason, outside the totals:

```console
Refused ~/code/app/node_modules: contains a Git repository at some-dep
```

With `--json`, each entry in `refused` has `path`, `project`, `rule`, and `reason`.
Swift Package Manager keeps Git checkouts and bare clones in `.build`,
so Plonge refuses that directory when it holds remote dependencies.
This protection also applies to worktrees with a `.git` file.
npm, pnpm, and Yarn 1 remove `.git` when installing packages from Git,
so those packages are not refused for that reason.
For Swift packages, `swift package clean` removes build output
but keeps the checkouts; `swift package reset` removes them too.

## Project discovery and activity

Plonge reads mise's loaded config records from `$MISE_STATE_DIR/tracked-configs`,
`$XDG_STATE_HOME/mise/tracked-configs`, or `~/.local/state/mise/tracked-configs`.
It skips global and system configs and unreadable or broken links.
It removes duplicate roots and roots already covered by another Git work tree root.
Nested repositories use their own Git checks but share the root's activity status.
The scan does not follow directory symlinks or enter a matched cache.
Rules can also exclude directory names from the scan.

Activity includes the modification time of the root's Git HEAD log,
or of `HEAD` if the log is unavailable,
and Claude Code transcript times under `~/.claude/projects/`
or `$CLAUDE_CONFIG_DIR/projects`.
The newest available time wins.
Uncommitted edits and builds do not update the Git activity time,
so a quiet status does not prove that a project is unused.

Claude transcript matching includes the root and its descendants.
Encoded names can also match a sibling such as `app-two` for a root named `app`;
this can only make the root appear more active.
A missing or unreadable transcript directory is reported as a skipped source.

## Global caches

Shared package-manager caches live outside projects.
To inspect them and preview their cleanup actions:

```console
plonge scan --global
plonge clean --global
```

`scan --global` adds global caches to the project report.
`clean --global` selects only global actions.
It cannot be combined with `--root`, `--no-mise`, `--include-active`, or `--min-size`.
Lookup and check commands run even during a scan or dry run.

Global rules have two kinds.
A `prune` rule uses the tool's policy to select unused or stale data,
such as `mise cache prune` or `uv cache prune`.
A `clear` rule can remove cached data still in use and needs `--include-clear`.
Later commands may have to download dependencies or rebuild files.

After reviewing the corresponding dry run, use `--apply`:

```console
plonge clean --global --apply
```

To include clear actions, preview and apply with the same flags:

```console
plonge clean --global --include-clear
plonge clean --global --include-clear --apply
```

Global cleanup does not use project activity or Git checks for tool commands.
A `delete_contents` rule instead deletes the contents of a specific directory
and keeps the directory itself.
These rules require `--include-clear` and refuse unsafe paths,
including the home directory, symlinks, and paths in or containing a Git repository.
They also check for protected names, such as Maven's `maven-metadata-local.xml`.

Plonge runs tools from your home directory, without a shell.
It uses `mise exec --` when `mise which` finds the tool there;
otherwise it uses `PATH`, passing over mise's shims.
Missing tools are skipped and reported.
Project-specific tool settings may therefore differ from those used for cleanup.

The `mise-tools` rule checks for tracked configs that mise cannot load
and skips cleanup if they may still exist.
Otherwise, it reports the versions that `mise ls --prunable --json` lists.
The version list and the dry run are separate calls, so their results can change.
If the version list fails, Plonge reports the size of mise's installs directory
and adds a note.
The check does not prove that mise knows about every project.

The default global rules also protect data within shared caches.
The Cargo rule leaves Git checkouts alone and clears only `registry`.
The Deno rules select module caches and leave localStorage and REPL history alone.
The Maven rule refuses a repository with locally installed artifacts,
because a remote repository may not have another copy.
Global cleanup can still interrupt a build that is using a selected cache.

The built-in Xcode rule on macOS selects only build directories whose
`info.plist` names a workspace that no longer exists.
It keeps directories with unreadable metadata or workspaces on unmounted volumes.

See the [rule reference](docs/rules.md#global-caches) for all global actions,
path resolution, checks, and overrides.
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
Choose only directories that your tools can restore.
Git cannot protect edits inside an ignored dependency folder
that has no repository of its own.
A name match alone does not establish that those files are disposable.

Rules can match paths such as `vendor/bundle`,
where the project file must be beside the first directory, `vendor`.
They can also require project-file patterns such as `*.csproj`.
Use `refuse_inside` to protect files within a cache that a rebuild cannot restore.
The default `_build` rule, for example, refuses `*/rel` and `*/lib/*/src`.

The scan skips `.cache`, `.git`, `.hg`, `.jj`, and `.svn` by default.
A name in both `names` and `skip` is reported as a cache but is not entered.
Version-control directories cannot be added to `names`, even as part of a path.

`names` and `skip` add to the defaults.
Each `beside` or `refuse_inside` entry replaces the default for that name;
`quiet_hours` replaces the default period.
A global rule with the same `id` replaces the entire default rule,
including its checks.
See the [rule reference](docs/rules.md) for the full format and examples,
and the [default rules](internal/rules/default.toml) before replacing an entry.

## Disk space

Reported cache sizes estimate the space that cleanup could free.
On Unix, Plonge uses allocated blocks and counts hard links once per cache;
on other platforms, it uses file lengths.
Files linked elsewhere, filesystem snapshots, and concurrent changes
can affect the space freed, and global size totals can overlap.


`plonge disk` prints the free space on your home directory's volume.

```console
plonge disk
plonge disk --warn-below 20GiB
```

With a nonempty `--warn-below` value, Plonge prints a warning only when
free space is below that size.
Size and disk errors are written to standard error and exit 0 in this mode.
Command-line syntax errors still exit 2.
Sizes accept `KiB`, `MiB`, `GiB`, and `TiB`
(and `K`, `M`, `G`, and `T`) as powers of 1024,
and `KB`, `MB`, `GB`, and `TB` as powers of 1000.

## MCP server

`plonge mcp` serves Plonge to coding agents over standard input and output
(stdio), with three tools:

- `scan` reports caches; `global: true` adds the global caches.
- `clean` is a dry run unless `apply: true`.
  It takes `include_active`, `min_size`, `global`, and `include_clear`,
  with the same rules as the command line.
- `disk` reports free space.

The MCP tools use mise project discovery and do not accept `--root` or `--no-mise`.
Their descriptions tell agents to show the dry run
and get user approval before `apply`.
The server does not enforce that approval.

To add the server to Claude Code:

```console
claude mcp add plonge -- plonge mcp
```

## Agent skill

`skills/plonge/SKILL.md` tells coding agents when to use Plonge
(low disk space, cleanup requests), to scan first, to show sizes,
and to ask you before `clean --apply`.
To install it for Claude Code, copy `skills/plonge` to `~/.claude/skills/plonge`.

## Hooks

This Claude Code `SessionStart` hook warns when free space is low.
It supplies a size to `--warn-below` so size and disk errors do not fail the hook.
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

Claude Code adds the hook's output to the session,
so the agent sees the warning and can offer to run the skill.

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
