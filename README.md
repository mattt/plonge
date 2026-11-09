# Plonge

Plonge finds developer caches, downloaded dependencies, and build output
that its rules can clean up.
It pairs with [mise](https://mise.jdx.dev/):
mise en place is the preparation, and the plonge is where the dishes get done.

`plonge scan` and `plonge clean` without `--apply` delete nothing.
`plonge clean --apply` deletes selected project cache directories
only if Git ignores them and they contain no tracked files.
It excludes active and busy projects by default.
`plonge clean --global --apply` instead runs global cleanup actions,
which do not use those project checks.

See [What Plonge covers](docs/coverage.md) for tool coverage and known gaps.

## Install

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

Plonge gets project directories from mise's record of loaded config files.
A root is a directory to scan.
The report calls each root a project, including any directories below it.
Use `--root` to add a root, or `--no-mise` to use only the roots you give.
Each root must be in a Git work tree.
For example, from a project's directory:

```console
plonge scan --no-mise --root .
```

A cache is a directory whose name matches a rule,
such as `node_modules` or `.venv`.
Plonge reports it only if Git ignores it and it contains no tracked files.
A rule defines a cache name, any required project file,
or a global cleanup action.

A quiet project has a known last activity time at least `quiet_hours` old
(24 hours by default).
An active project has more recent activity or an unknown activity time.
A busy project has a Paseo agent with status `running` or `initializing`
in the project or a directory below it.
Busy projects are excluded from project cleanup even with `--include-active`.
These states depend on the [activity sources](#project-discovery-and-activity),
not on whether a build or editor is running.

`scan` lists sizes, projects, last activity, activity sources, and matched rules.
`clean` shows which caches qualify for deletion.
Use `--json` with either command for structured output.

## Delete project caches

Review the dry run, then add `--apply` with the same selection options:

```console
plonge clean
plonge clean --apply
```

By default, only caches in quiet projects qualify.
`--include-active` also selects active projects.
`--min-size 100MiB` skips caches smaller than 100 MiB.
You can repeat `--root`; `--no-mise` requires at least one root.

Before each deletion, Plonge checks that the path is still a directory,
is not a symlink, is inside its root,
is ignored by Git, and contains no tracked files.
It asks Paseo again once per project, before the first deletion there.
If a path check does not pass, Plonge skips the path or reports an error.
Plonge can make read-only files and directories writable to remove them.
It continues after deletion errors and exits with a nonzero status.

The report estimates the space freed; it does not measure volume free space.
On Unix, sizes use allocated blocks and count a hard-linked file once per cache.
On other platforms, sizes use file lengths.
Files linked elsewhere, filesystem snapshots, and changes during cleanup
can affect how much space becomes available.
Global size totals can also overlap.
Use `plonge disk` to check free space on your home directory's volume.

## Project discovery and activity

mise records loaded config files in `$MISE_STATE_DIR/tracked-configs`,
or `$XDG_STATE_HOME/mise/tracked-configs`,
with `~/.local/state/mise/tracked-configs` as the default.
Plonge maps each config path to a project root.
It skips global and system configs and unreadable or broken links,
and removes duplicate roots and roots inside another root.
A root outside a Git work tree is skipped,
even if it contains Git repositories.

The scan does not follow directory symlinks or enter a matched cache.
The [rules](#rules) also exclude some directory names from the scan.
Nested Git repositories use their own ignore and tracked-file checks,
but share the root's activity status.

Plonge uses the newest activity it can find from these sources:

- `git`: the modification time of the root's Git HEAD log,
  or of `HEAD` if the log is unavailable.
  Uncommitted edits and build processes do not update this activity time.
- `claude`: the newest transcript modification time under
  `~/.claude/projects/`, or `$CLAUDE_CONFIG_DIR/projects`.
  Plonge matches encoded directory names for the root and its descendants.
  This can also match a sibling such as `app-two` for a root named `app`,
  which can only make the root appear more active.
- `paseo`: `paseo ls --json --global --background`.
  A busy project gets the scan time as its last activity time.

If Claude or Paseo is missing or its initial query fails,
Plonge reports that source as skipped and continues.
If Paseo is unavailable, Plonge cannot detect busy projects through it.
A failed Paseo recheck before deletion also allows cleanup to continue,
but is not reported.

## Global caches

Global caches are stored outside projects.
Plonge uses cleanup commands from their tools,
plus a built-in rule for Xcode build data on macOS.
`plonge scan --global` adds each rule's path, size, kind, and cleanup action.

```console
plonge scan --global
plonge clean --global
plonge clean --global --apply
plonge clean --global --apply --include-clear
```

`plonge clean --global` works only on global caches and leaves your projects alone.
By default it lists the commands as a dry run.
`--apply` runs eligible actions and estimates the space freed.
Global cleanup does not use the project activity or Git checks.
It cannot be combined with `--root`, `--no-mise`, `--include-active`, or `--min-size`.
Lookup and check commands run even during a scan or dry run.

Each rule has a kind.
A `prune` rule uses the tool's policy to select unused or stale data.
For example, `mise cache prune` uses cache age.
A `clear` rule can remove cached data still in use
and requires `--include-clear`.
Later commands may need to download dependencies or rebuild files.
Neither kind guarantees that the whole reported size will be freed.

| ID | Kind | Action |
| --- | --- | --- |
| `mise-tools` | prune | `mise prune --tools --yes` |
| `mise-cache` | prune | `mise cache prune` |
| `uv` | prune | `uv cache prune` |
| `pnpm` | prune | `pnpm store prune` |
| `go-modules` | clear | `go clean -modcache` |
| `go-build` | clear | `go clean -cache` |
| `npm` | clear | `npm cache clean --force` |
| `yarn` | clear | `yarn cache clean` (Yarn 1) |
| `pip` | clear | `pip cache purge` |
| `homebrew` | clear | `brew cleanup --prune=all` |
| `xcode-derived-data` | prune | Built-in deletion of stale Xcode build directories (macOS) |

Plonge runs a tool through `mise exec --`
when `mise which` finds it from your home directory,
and otherwise from `PATH`, passing over mise's shims.
It runs commands from your home directory, without a shell.
A rule whose tool is missing is skipped and reported.

mise does not count a tracked config that it cannot load,
so a prune can remove a version that such a config pins.
Plonge runs `mise prune --tools --dry-run` first,
and skips `mise-tools` when mise reports that it cannot load
a tracked config that may still exist.
The report and the JSON list those configs.
When the check passes, `mise-tools` reports the size of the versions
that mise lists as removable,
and the report names them.
The names and sizes come from `mise ls --prunable --json`.
That command and the dry run are separate calls; their results can change.
If the list command fails, Plonge reports the size of the configured
`installs` directory and adds a note.
The default path is `~/.local/share/mise/installs`.
If mise uses a different installs directory, override the rule
as shown below to correct the fallback size and freed-space estimate.
The cleanup command still uses mise's environment and settings.

On macOS, a built-in rule, `xcode-derived-data`, selects stale Xcode build data.
Xcode has no prune command,
so Plonge deletes only a directory in `~/Library/Developer/Xcode/DerivedData`
whose XML `info.plist` names an absolute `WorkspacePath` that no longer exists.
It keeps a directory without a readable `WorkspacePath`,
and a directory whose workspace is on a volume that is not mounted.

You can add a rule, or replace a default with the same `id`,
in `rules.toml`:

```toml
[[global]]
id = "mise-tools"
tool = "mise"
path = "~/data/mise/installs" # for a custom MISE_DATA_DIR
clean = "mise prune --tools --yes"
kind = "prune"
check = "mise prune --tools --dry-run"
check_env = ["MISE_QUIET=0", "MISE_LOG_LEVEL=info"]
refuse = 'error loading tracked config file (?P<path>.+)$'
refuse_reason = "tracked configs that mise cannot load"
targets = "mise ls --prunable --json"

[[global]]
id = "mytool"
tool = "mytool"
path = "mytool cache path"
clean = "mytool cache gc"
kind = "prune"
os = "darwin"
```

`tool` is the command that must be available.
`path` is a command that prints the cache's path,
or a fixed path that starts with `/` or `~/`.
Both `path` and `clean` commands must start with `tool`.
Command strings are split at whitespace.
Shell quoting, pipes, and variable expansion are not supported.
`os` limits a rule to one operating system,
by its Go name, such as `darwin` or `linux`.

A rule can name a `check`, a command that must not change anything:

```toml
[[global]]
id = "mytool"
tool = "mytool"
path = "mytool cache path"
clean = "mytool cache gc"
kind = "prune"
check = "mytool cache gc --dry-run"
refuse = 'cannot read project (?P<path>.+)$'
refuse_reason = "projects that mytool cannot read"
check_env = ["MYTOOL_LOG=info"]
```

`check` must start with `tool`.
Plonge runs it for `scan --global` and `clean --global`,
and again right before `--apply` runs the rule.
The rule is skipped when the check fails
or a line of its standard output or standard error matches `refuse`,
a regular expression.
A skipped rule does not count toward the totals.

A group named `path` in `refuse` limits matches to files that may still exist.
For absolute paths and `~/` paths, missing files do not count.
Permission errors and text that is not an absolute or `~/` path do count.
The group can run past the path to the end of the line:
Plonge tries the text up to each `: ` in turn, and then the whole text.
The report and the JSON (`refused`) list the files with `~`,
and the reason ends with how many there are.
`refuse_reason` is the text of that reason.
`check_env` sets variables for the check only.

`targets` is a command that prints what `clean` would remove,
as a JSON object that maps each name
to a list of `{"version": ..., "install_path": ...}` objects.
`targets` must start with `tool`.
Each object needs a nonempty `version` and an absolute `install_path`.
The rule's size is then the total of those directories, not its whole `path`.

When your rule has the same `id` as a default, it replaces all of the default,
so copy the default's `check` and `targets` to keep them.

## Disk

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

## Rules

Plonge has these default rules:

```toml
names = [".astro", ".build", ".gradle", ".mypy_cache", ".next", ".parcel-cache",
         ".pytest_cache", ".ruff_cache", ".svelte-kit", ".turbo", ".venv",
         "DerivedData", "__pycache__", "dist", "node_modules", "target", "venv"]
quiet_hours = 24

# Do not scan inside these directories.
skip = [".cache", ".git", ".hg", ".jj", ".svn"]

# Match these names only when a listed project file is in the same directory.
[beside]
dist = ["package.json"]
target = ["Cargo.toml", "pom.xml"]
venv = ["pyproject.toml", "requirements.txt", "setup.py"]
```

To add rules, write them to `$XDG_CONFIG_HOME/plonge/rules.toml`
(by default, `~/.config/plonge/rules.toml`).
Your `names` and `skip` add to the defaults.
A `beside` entry in your file replaces the default entry for that name,
and a `quiet_hours` value replaces the default.
For example:

```toml
names = [".cache", "build"]
quiet_hours = 72
skip = ["third_party"]

[beside]
build = ["build.gradle", "build.gradle.kts"]
dist = ["package.json", "pyproject.toml"]
```

The scan never enters a directory in `skip`.
This keeps out caches that are nested in a tool's own cache,
such as `__pycache__` inside a `.cache/uv` environment.
A name in both `names` and `skip` is reported as a cache and is not entered.
`.git`, `.hg`, `.jj`, and `.svn` cannot be added to `names`.

## Development

The repository pins its tools in `mise.toml`.

```console
mise install
mise exec -- make check
```

`make build` writes `bin/plonge`.
`make lint` runs `gofmt -l` and `go vet`.
`make test` runs the tests with gotestsum.
