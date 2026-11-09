# Plonge

Plonge finds the caches that developer tools and coding agents
leave on disk, with rules that you can extend.
It pairs with [mise](https://mise.jdx.dev/):
mise en place is the preparation, and the plonge is where the dishes get done.

`plonge scan` reads your projects and prints what it finds; it deletes nothing.
`plonge clean` deletes what it finds, and only when you give `--apply`.

## Install

With mise, through its `go` backend:

```console
mise use -g go:github.com/mattt/plonge/cmd/plonge@latest
```

With Go 1.26 or later:

```console
go install github.com/mattt/plonge/cmd/plonge@latest
```

Plonge runs `git`, so Git must be on your `PATH`.

## Scan

```console
plonge scan
```

Plonge gets your projects from mise.
mise records each config file that it loads
in `$MISE_STATE_DIR/tracked-configs`
(by default, `~/.local/state/mise/tracked-configs`).
Plonge maps each file to its project root,
skips mise's global config and links whose target is gone,
and drops a root that is inside another root.

Use `--root` to add a directory; you can give it more than once.
Use `--no-mise` to scan only the `--root` directories.

```console
plonge scan --root ~/Code/scratch
plonge scan --no-mise --root ~/Code
```

In each root, plonge looks for directories with a cache name,
such as `node_modules` or `.venv`.
It does not follow symlinks or descend into a cache that it found.
A directory counts as a cache only when Git ignores it
and no tracked file is inside it.
A root that is not in a Git work tree is skipped.

The report lists each cache with its size on disk, its project,
the project's last activity and its source, whether the project is quiet,
and the rule that matched.
A project is quiet when no source has seen activity
for `quiet_hours` (by default, 24 hours).
The totals at the end give the size of all caches and of quiet ones.

Plonge reads activity from these sources and reports which one set each project's last activity:

- `git`: the modification time of the project's Git HEAD log, else of `HEAD`.
- `claude`: the newest transcript in `~/.claude/projects/`
  (or `$CLAUDE_CONFIG_DIR/projects`) for the project or a directory below it.
  Claude Code names each folder by replacing every character except letters and digits
  with a hyphen, so a sibling such as `app-two` can also count for `app`.
  That can only make a project look more active.
- `paseo`: `paseo ls --json --global --background`.
  A project with a `running` or `initializing` agent at or below it is busy.
  Plonge shows `busy` in the report and never deletes in that project,
  even with `--include-active`.

A source that is missing or fails is skipped, and the report says so.

Use `--json` to get the same data as JSON.

## Clean

```console
plonge clean
plonge clean --apply
```

`plonge clean` selects caches as `scan` does and takes the same
`--root`, `--no-mise`, and `--json` options.
By default it is a dry run: it prints what it would delete and the total.
`--apply` deletes.

Only caches in quiet projects qualify.
`--include-active` adds the others,
and `--min-size` (such as `--min-size 100MiB`) leaves out smaller caches.

Right before each deletion, plonge checks again that the path is still a directory,
is not a symlink, is inside its project,
is ignored by Git, and holds no tracked file.
It also asks Paseo again, once per project, before it deletes there.
If a check does not pass, plonge skips the path and reports the reason.
Plonge makes read-only files and directories writable when removal needs it,
as Go's module cache does.
After an error, it reports the error, goes on, and exits with a non-zero status.
It ends with the space freed.
Sizes count each hard-linked file once,
so a cache whose files are also linked elsewhere can free less than its size.

## Global caches

Some caches live outside your projects, and the tool that owns each one
knows best how to clean it.
`plonge scan --global` adds a section with each rule's path, size, kind, and command.

```console
plonge scan --global
plonge clean --global
plonge clean --global --apply
plonge clean --global --apply --include-clear
```

`plonge clean --global` works only on global caches and leaves your projects alone.
By default it lists the commands as a dry run.
`--apply` runs them, measures each cache again, and reports what was freed.

Each rule has a kind.
A `prune` rule removes only what nothing uses.
A `clear` rule removes everything, so the next build downloads it again;
it runs only with `--include-clear`.

| ID | Kind | Command |
| --- | --- | --- |
| `mise-tools` | prune | `mise prune --tools` (`mise ls --prunable` lists the versions) |
| `mise-cache` | prune | `mise cache prune` |
| `uv` | prune | `uv cache prune` |
| `pnpm` | prune | `pnpm store prune` |
| `go-modules` | clear | `go clean -modcache` |
| `go-build` | clear | `go clean -cache` |
| `npm` | clear | `npm cache clean --force` |
| `yarn` | clear | `yarn cache clean` (Yarn 1) |
| `pip` | clear | `pip cache purge` |
| `homebrew` | clear | `brew cleanup --prune=all` |

Plonge runs a tool through `mise exec --` when `mise which` finds it from your home directory,
and otherwise from `PATH`, passing over mise's shims.
It runs commands from your home directory, without a shell.
A rule whose tool is missing is skipped and reported.

On macOS, a built-in rule, `xcode-derived-data`, cleans stale Xcode build data.
Xcode has no prune command,
so plonge deletes only a folder in `~/Library/Developer/Xcode/DerivedData`
whose `info.plist` names a `WorkspacePath` that no longer exists.
It keeps a folder without a readable `WorkspacePath`,
and a folder whose workspace is on a volume that is not mounted.

You can add a rule, or replace a default with the same `id`,
in `rules.toml`:

```toml
[[global]]
id = "mise-tools"
tool = "mise"
path = "~/data/mise/installs" # for a custom MISE_DATA_DIR
clean = "mise prune --tools"
kind = "prune"

[[global]]
id = "mytool"
tool = "mytool"
path = "mytool cache path"
clean = "mytool cache gc"
kind = "prune"
os = "darwin"
```

`tool` is the command that must be available.
`path` is a command that prints the cache's path, or a fixed path that starts with `/` or `~/`.
Both `path` and `clean` commands must start with `tool`.
`os` limits a rule to one operating system, by its Go name, such as `darwin` or `linux`.

## Disk

`plonge disk` prints the free space on your home directory's volume.

```console
plonge disk
plonge disk --warn-below 20GiB
```

With `--warn-below`, plonge prints one warning line only when free space
is below that size, and it always exits 0, so that hooks can call it.
Sizes accept `KiB`, `MiB`, `GiB`, and `TiB` (and `K`, `M`, `G`, and `T`) as powers of 1024,
and `KB`, `MB`, `GB`, and `TB` as powers of 1000.

## MCP server

`plonge mcp` serves plonge to coding agents over stdio, with three tools:

- `scan` reports caches; `global: true` adds the global caches.
- `clean` is a dry run unless `apply: true`.
  It takes `include_active`, `min_size`, `global`, and `include_clear`,
  with the same rules as the command line.
- `disk` reports free space.

The tool descriptions tell agents to show the dry run to the user
and get approval before `apply`.

To add the server to Claude Code:

```console
claude mcp add plonge -- plonge mcp
```

## Agent skill

`skills/plonge/SKILL.md` tells coding agents when to use plonge
(low disk space, cleanup requests), to scan first, to show sizes,
and to ask you before `clean --apply`.
To install it for Claude Code, copy `skills/plonge` to `~/.claude/skills/plonge`.

## Hooks

`plonge disk --warn-below` always exits 0, so a hook can call it at any time.
This Claude Code `SessionStart` hook warns when free space is low.
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

# Directories that the walk never enters.
skip = [".cache", ".git", ".hg", ".jj", ".svn"]

# A generic name counts only next to one of these project files.
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

The walk never enters a directory in `skip`.
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
`make lint` runs `gofmt -l` and `go vet`, and `make test` runs the tests with gotestsum.
