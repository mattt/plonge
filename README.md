# Plonge

Plonge finds developer caches, downloaded dependencies, and build output
that its rules can clean up.
It pairs with [mise](https://mise.jdx.dev/):
mise en place is the preparation, and the plonge is where the dishes get done.

`plonge scan` and `plonge clean` without `--apply` delete nothing.
`plonge clean --apply` deletes selected project cache directories
only if Git ignores them, they contain no tracked files,
they are not and do not hold a Git repository
other than the dependency clones that Swift Package Manager can restore,
and they hold nothing that their rule protects.
It excludes active and busy projects by default.
`plonge clean --global --apply` instead runs global cleanup actions,
which do not use those project checks.

See [What Plonge covers](docs/coverage.md) for tool coverage and known gaps.

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

Plonge gets project directories from mise's record of loaded config files.
A root is a directory to scan.
The report calls each root a project, including any directories below it.
Use `--root` to add a root, or `--no-mise` to use only the roots you give.
Plonge scans a root only if it is in a Git work tree.
It skips and reports a root outside one,
but still scans the roots inside it, such as mise projects.
For example, from a project's directory:

```console
plonge scan --no-mise --root .
```

A cache is a directory whose name matches a rule,
such as `node_modules` or `.venv`.
Plonge reports it only if Git ignores it and it contains no tracked files.
It refuses a cache that is or holds a Git repository,
as [described below](#caches-that-hold-a-git-repository),
and a cache that holds an entry that its rule protects,
such as a release or dependency sources in a Mix `_build` directory.
A rule defines a cache name, any required project file,
or a global cleanup action.

A quiet project has a known last activity time at least `quiet_hours` old
(24 hours by default).
An active project has more recent activity or an unknown activity time.
A busy project has a Paseo agent with status `running` or `initializing`
in the project or a directory below it.
If `paseo` is on PATH but Plonge cannot read its status,
every project is busy, as [described below](#paseo-failures).
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
is ignored by Git, contains no tracked files,
is not and does not hold a Git repository,
other than [SwiftPM's unchanged clones](#swift-package-manager-checkouts),
and holds no entry that its rule protects.
It asks Paseo again once per project, before the first deletion there,
and skips the project's caches if Paseo now reports an agent there
or if Plonge cannot read Paseo's status.
If a path check does not pass, Plonge skips the path or reports an error.
Plonge can make read-only files and directories writable to remove them.
It continues after deletion errors and exits with a nonzero status.

### Disk space

After `--apply`, the report gives two numbers:

```console
Estimated freed: 29.5 GiB (from cache sizes)
Free space on ~ grew by 14.0 GiB, from 80.1 GiB to 94.1 GiB; other processes and snapshots can change it
```

The estimate adds up the sizes of the caches that Plonge deleted or cleaned,
measured as described below.
The second line is the change in free space on your home directory's volume,
which Plonge reads before the first deletion and after the last one.
It is what became available on that volume, but other processes
write and delete files at the same time,
and caches on another volume do not change it.
With `--json`, `home_volume` has `free_before`, `free_after`, and `gained_bytes`,
or an `error` when Plonge cannot read free space.
Dry runs do not have these values.

A size is the space that deleting the cache would free,
as well as Plonge can measure it.
On Unix, sizes use allocated blocks and count a hard-linked file once per cache.
On other platforms, sizes use file lengths.

On macOS, a file can be a clone that shares its blocks with other files,
as when uv clones files from its cache into a virtual environment,
or when a snapshot, such as a Time Machine local snapshot, keeps them.
Deleting such a file frees only the blocks that no other file
or snapshot uses.
So on macOS, Plonge counts a file's private size,
which APFS reports through `getattrlist` (`ATTR_CMNEXT_PRIVATESIZE`),
in place of its allocated blocks.
When a file and all of its full clones are in the same cache,
Plonge also counts the blocks that they share, once,
because deleting the cache frees them.
That count includes blocks that a snapshot also keeps,
and blocks that the family shares with a clone that was changed after it was made,
which APFS no longer counts as a full clone.
Deleting the cache does not free those blocks,
so its size can be larger than the space that it frees.
This count is never larger than the allocated blocks.
A compressed file, and a file on a volume that does not report a private size,
use allocated blocks.
For these reasons, a size on macOS can be smaller than what `du` reports.

Files linked elsewhere, snapshots on other systems, and changes during cleanup
can affect how much space becomes available.
Global size totals can also overlap.
Use `plonge disk` to check free space on your home directory's volume.

### Caches that hold a Git repository

A cache can hold a separate Git repository,
such as a dependency checked out from Git or a clone in a `build` directory.
The enclosing repository ignores the cache and does not see that repository's files,
so its ignore and tracked-file checks cannot protect its commits or uncommitted work.
Plonge refuses a cache that has a `.git` entry of any type
(a directory, a worktree-style file, or a symlink) at any depth,
or a Git directory (`HEAD` beside `objects` and `refs`), such as a bare clone.
It also refuses a cache with a directory that it cannot read,
because that directory could hide a repository.
The only exception is the clones that Swift Package Manager manages,
as [described below](#swift-package-manager-checkouts).
Plonge stops at the first repository that it finds,
does not follow symlinks, and checks while it measures the cache's size.
It checks again right before deletion.

`scan` and `clean` list each refused cache and the reason,
and do not count it in the totals:

```console
Refused ~/code/app/node_modules: contains a Git repository at some-dep
```

With `--json`, the `refused` list has the `path`, `project`, `rule`, and `reason`
of each one.

npm, pnpm, and Yarn 1 do not keep `.git` when they install a package from Git,
so such a `node_modules` is not refused for that reason.

### Swift Package Manager checkouts

Swift Package Manager keeps a clone of each remote dependency
in `.build/checkouts/<identity>`
and a bare clone in `.build/repositories/<name>`.
`swift package resolve` makes both again at the commits in `Package.resolved`.
For a `.build` directory beside `Package.swift`,
Plonge does not refuse these repositories when SwiftPM can make them again:

- A bare clone directly in `repositories` must be bare,
  have no linked worktrees, and have one remote, `origin`,
  set up as a mirror of a `location` that a pin in `Package.resolved` names.
  Two URLs with a scheme or an scp-style host, such as `git@host:path`,
  must be equal byte for byte, except for one trailing slash;
  two absolute paths must resolve, through symlinks, to the same directory.
  Plonge refuses a relative path in either one.
  No commit may be held only by its reflogs.
- A checkout directly in `checkouts` must have a `.git` directory,
  not a `.git` file or symlink, and one remote, `origin`,
  that is an accepted bare clone in the same cache.
  Its `HEAD` must be the commit that `Package.resolved` pins for its identity,
  and `git status` must show no changed, untracked, or ignored files.
  It must have no stash, no linked worktrees,
  no files marked assume-unchanged or skip-worktree,
  and no reflog entry for a push to `origin`.
  Every commit in it must be in `HEAD`, in `refs/remotes/origin`,
  or in a tag that the bare clone holds at the same object.
  A local branch, a tag that only the checkout has,
  or a reflog does not count.

Plonge refuses the `.build` directory for any other Git repository in it,
including a repository inside a checkout, such as a submodule,
and the reason names the path:

```console
Refused ~/code/app/.build: contains a Git repository at checkouts/swift-nio that has changed, untracked, or ignored files
```

Plonge reads `Package.resolved` versions 2 and 3, which key pins by package identity.
It refuses a checkout when it cannot read the file,
including a file in the version 1 format.
It runs Git with the same environment as its other Git checks,
names each repository explicitly, and sets every `git status` option
that configuration could otherwise change.
It checks when it measures the cache and again right before deletion.

These checks run offline, so they cannot tell a commit that someone pushed
into a bare clone from one that SwiftPM fetched.
Plonge reads only the reflogs of the checkout's `refs/remotes/origin` refs,
where a branch push leaves an entry.
A tag push leaves no entry there, so a local tag pushed from the checkout
to its bare clone is not detected, and neither is a push into the bare clone
from a repository outside the cache.
A SwiftPM mirror set in `.swiftpm/configuration/mirrors.json`
clones a URL that differs from the pin's `location`, so its clone is refused.

A `DerivedData` directory beside `Package.swift`,
such as one that `xcodebuild -derivedDataPath DerivedData` makes,
has the same layout in `SourcePackages`, and Plonge checks it the same way.
A `DerivedData` directory beside an Xcode project or workspace
is still refused when it holds package clones,
because Xcode keeps those pins inside the project or workspace,
and one folder can hold several of them.

`swift package reset` removes `.build`;
`swift package clean` removes only the build output and keeps the checkouts.

## Project discovery and activity

mise records loaded config files in `$MISE_STATE_DIR/tracked-configs`,
or `$XDG_STATE_HOME/mise/tracked-configs`,
with `~/.local/state/mise/tracked-configs` as the default.
Plonge maps each config path to a project root.
It skips global and system configs and unreadable or broken links,
and removes duplicate roots.
It also removes a root inside another root that is in a Git work tree,
because the scan of that root includes it.
A root outside a Git work tree is skipped,
and the scan does not look for Git repositories inside it.
A root inside a skipped root is still scanned,
so a `--root` folder that holds several repositories
does not hide the mise projects in those repositories.

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
  Plonge expands a leading `~` in an agent's working directory
  to the home directory.
  A busy project gets the scan time as its last activity time.

If Claude Code's projects directory is missing or unreadable,
Plonge reports the `claude` source as skipped and continues.
If `paseo` is not on PATH, Plonge cannot ask Paseo,
so it does not know of any running Paseo agent.
It reports the `paseo` source as skipped, and no project is busy.

### Paseo failures

If `paseo` is on PATH but `paseo ls` fails, does not finish in 15 seconds,
or prints output that Plonge cannot use,
Plonge cannot tell where Paseo agents work.
Output that Plonge cannot use includes output that is not a JSON array,
and a running or initializing agent whose working directory is empty
or is not an absolute path after `~` expansion.
It reports the `paseo` source as failed, with the reason,
and treats every project as busy.
`scan` shows every project as busy,
and `clean` selects no project caches, with or without `--apply`:

```console
$ plonge clean
Not selected, busy project (Paseo): 1.2 GiB in 14 caches
Activity source failed: paseo (paseo ls failed: daemon is not running)
Every project counts as busy. Fix paseo, or remove it from PATH for this run.
Selected size: 0 B in 0 caches
Dry run; nothing was deleted. Use --apply to delete.
```

With `--json`, the `activity_sources` list has
`{"name": "paseo", "status": "failed", "reason": "..."}`.
If the recheck before a deletion fails in the same way,
Plonge skips that project's caches and reports the reason for each one.

To proceed, fix Paseo so that `paseo ls --json --global --background` works.
If no Paseo agent runs on this machine,
you can instead remove `paseo` from PATH for that run.
Plonge then cannot detect busy projects through Paseo.

## Global caches

Global caches are stored outside projects.
Plonge uses cleanup commands from their tools,
plus a built-in rule for Xcode build data on macOS.
A rule for a cache without a cleanup command,
or whose command also removes user state,
can delete the contents of its path instead,
as `zig-global-cache` does for Zig, `rubygems-cache` does for RubyGems,
and as the Deno, Cargo, Gradle, Maven, `hex-packages`, and `rebar3-hex` rules do.
`plonge scan --global` adds each rule's path, size, kind, and cleanup action.

```console
plonge scan --global
plonge clean --global
plonge clean --global --apply
plonge clean --global --apply --include-clear
```

`plonge clean --global` works only on global caches and leaves your projects alone.
By default it lists the commands as a dry run.
`--apply` runs eligible actions, estimates the space freed,
and reports the change in free space on your home directory's volume,
as [described above](#disk-space).
Global cleanup does not use the project activity or Git checks,
except that a `delete_contents` rule refuses a path
that is in or holds a Git repository.
It cannot be combined with `--root`, `--no-mise`, `--include-active`, or `--min-size`.
Lookup and check commands run even during a scan or dry run.

Each rule has a kind.
A `prune` rule uses the tool's policy to select unused or stale data.
For example, `mise cache prune` uses cache age.
A `clear` rule can remove cached data still in use
and requires `--include-clear`.
Later commands may need to download dependencies or rebuild files.
Neither kind guarantees that the whole reported size will be freed.

For most prune rules, the size is the size of the whole cache,
and the tool's policy selects which part of it to remove,
so the space that a prune frees can be much smaller.
`scan --global` and `clean --global` mark such a size `(whole cache)`,
and with `--json`, the rule has `"whole_cache": true`.
A prune rule that lists what it removes, as `mise-tools` does,
and `xcode-derived-data` report the size of what they would remove instead,
without the mark.
A clear rule's size is the size of its cache, without the mark.

| ID | Kind | Action |
| --- | --- | --- |
| `mise-tools` | prune | `mise prune --tools --yes` |
| `mise-cache` | prune | `mise cache prune` |
| `uv` | prune | `uv cache prune` |
| `pnpm` | prune | `pnpm store prune` |
| `go-modules` | clear | `go clean -modcache` |
| `go-build` | clear | `go clean -cache` |
| `cargo-registry` | clear | Built-in deletion of the contents of `registry` in the Cargo home |
| `npm` | clear | `npm cache clean --force` |
| `bun` | clear | `bun pm cache prune` (Bun 1.4.4 or later) |
| `deno-remote`, `deno-npm`, `deno-gen` | clear | Built-in deletion of the contents of Deno's module, npm, and TypeScript caches |
| `yarn` | clear | `yarn cache clean` (Yarn 1) |
| `yarn-berry` | clear | `yarn cache clean` (Yarn 2 and later) |
| `pip` | clear | `pip cache purge` |
| `homebrew` | clear | `brew cleanup --prune=all` |
| `nuget-packages` | clear | `dotnet nuget locals global-packages --clear` |
| `nuget-http-cache` | clear | `dotnet nuget locals http-cache --clear` |
| `swiftpm-macos` | clear | `swift package purge-cache` (macOS) |
| `swiftpm-linux` | clear | `swift package purge-cache` (Linux) |
| `xcode-derived-data` | prune | Built-in deletion of stale Xcode build directories (macOS) |
| `zig-global-cache` | clear | Built-in deletion of the contents of Zig's global cache |
| `hex-packages` | clear | Built-in deletion of the contents of `~/.hex/packages` |
| `rebar3-hex` | clear | Built-in deletion of the contents of `~/.cache/rebar3/hex` |
| `rubygems-cache` | clear | Built-in deletion of the contents of RubyGems' gem cache |
| `gradle-caches` | clear | Built-in deletion of the contents of `caches` in the Gradle user home |
| `maven-repository` | clear | Built-in deletion of the contents of `~/.m2/repository`, refused when it holds an artifact that `mvn install` put there |

Plonge runs a tool through `mise exec --`
when `mise which` finds it from your home directory,
and otherwise from `PATH`, passing over mise's shims.
It runs commands from your home directory, without a shell.
A rule whose tool is missing is skipped and reported.
The SwiftPM rules add `--scratch-path`, relative to the home directory,
so that `purge-cache` writes its `.build` marker files
in SwiftPM's cache directory and does not create `~/.build`.

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
If the list command fails, Plonge reports the size of mise's installs
directory and adds a note.
Plonge also measures that directory to estimate the space freed.
It uses `$MISE_INSTALLS_DIR`, or the `installs` directory
in the data directory that `mise doctor --json` reports,
so a custom `MISE_DATA_DIR` is followed.
If mise cannot report its data directory,
Plonge uses the rule's `path`, `~/.local/share/mise/installs` by default,
and adds a note.

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
path = "~/.local/share/mise/installs"
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
Plonge uses the last line that the command prints.
If the tool prints a label before the path, set `path_label`:
with `path_label = "global-packages"`, Plonge uses the text after
`global-packages: ` on the last line that contains it,
so a line such as `info : global-packages: /path/` gives `/path/`.
Plonge skips the rule when no line has the label,
or when two lines with the label give different paths.
A `delete_contents` rule can also use `path_label`;
the labeled path gets the same checks as any other path.
If the command prints a JSON object instead,
set `path_field` to the name of the string field that holds the path,
such as `path_field = "npmCache"` for `deno info --json`.
Both `path` and `clean` commands must start with `tool`.
Command strings are split at whitespace.
Shell quoting, pipes, and variable expansion are not supported.
`os` limits a rule to one operating system,
by its Go name, such as `darwin` or `linux`.

When the `path` command prints a ZON struct, as `zig env` does,
`path_zon_field` names the top-level field that holds the path:

```toml
path = "zig env"
path_zon_field = "global_cache_dir"
```

A relative path in that field is relative to the home directory,
where Plonge runs the command.
Plonge reads the whole struct before it uses the path.
It skips the rule when the output is not one complete ZON struct,
has text other than whitespace and comments after it,
has a field twice, has no such field,
or has a value that is not a nonempty string.
A rule can set only one of `path_label`, `path_field`, and `path_zon_field`,
and only with a `path` command.

A fixed `path` can give way to an environment variable
that holds the tool's directory.
`path_env` names the variable:

```toml
path = "~/.cargo"
path_env = "CARGO_HOME"
path_subdir = "registry"
```

When the variable is set and not empty, Plonge uses its value;
otherwise it uses `path`.
Plonge reads the variable from its own environment,
not from the environment that `mise exec` gives the tool.
It skips the rule when the value is not an absolute path.

When the cache is inside the directory that a rule resolves,
`path_subdir` names the cache's relative path below it.
Plonge joins it to whichever path the rule resolves:
a fixed `path`, the value of `path_env`, or the path that a `path` command prints.
In the example above, the cache is `$CARGO_HOME/registry` or `~/.cargo/registry`.
For RubyGems, which prints the directory above its cache:

```toml
path = "gem env gemdir"
path_subdir = "cache"
```

`path_subdir` must be a relative path of directory names:
it cannot start with `/`, end with `/`, or contain `.` or `..`.

Cargo's command that cleans its global cache, `cargo clean gc`, is unstable,
so the `cargo-registry` rule deletes the contents
of `registry` in the Cargo home: `$CARGO_HOME`, or `~/.cargo`.
It keeps `git`, whose checkouts are working trees that a user can edit,
and `bin`, `config.toml`, `credentials.toml`, and the records of `cargo install`.
Cargo 1.88 and later also delete unused files in these folders on their own.

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
`require` is also a regular expression;
when it is set, the rule is skipped unless exactly one line of that output
matches it.
For example, `check = "yarn --version"` with `require = '^1\.'`
limits a rule to Yarn 1.
A skipped rule does not count toward the totals.

A rule can add more checks, each with the same keys,
in `[[global.checks]]` tables after the rule.
The rule must have its own `check` first.
Plonge runs the checks in order and skips the rule at the first one
that does not pass:

```toml
[[global]]
id = "yarn-berry"
tool = "yarn"
path = "yarn config get cacheFolder"
clean = "yarn cache clean"
kind = "clear"
check = "yarn --version"
require = '^[0-9]+\.[0-9]+\.[0-9]+'
refuse = '^[01]\.[0-9]+\.[0-9]+'
refuse_reason = "yarn --version did not report Yarn 2 or later"

[[global.checks]]
check = "yarn config get enableGlobalCache"
require = '^true$'
```

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

A cache with no cleanup command can use `delete_contents` in place of `clean`:

```toml
[[global]]
id = "mytool-cache"
tool = "mytool"
path = "~/.mytool/cache"
delete_contents = true
kind = "clear"
```

Plonge then deletes what is in `path` and keeps the directory itself.
The default `zig-global-cache` rule gets its path from `zig env`.
Zig has no command that cleans this cache.
It holds build output and the packages that `zig fetch` and `zig build` download,
so later builds can be slower and must download packages again.
Before Zig 0.15, `zig env` printed JSON, so Plonge skips this rule;
replace it with a fixed `path` to use it with those versions.
The default `hex-packages` and `rebar3-hex` rules use fixed paths,
because neither Hex nor Rebar3 has a command that prints them.
They delete only the package folders,
and keep the Hex configuration and the `rebar3` that `rebar3 local install` makes.
A package cache moved by `HEX_HOME`, `MIX_XDG`, or `REBAR_CACHE_DIR`
is not covered; replace the rule with that `path` to use it.
The default `gradle-caches` rule deletes the contents of `caches`
in `$GRADLE_USER_HOME`, or in `~/.gradle` when the variable is not set.
It keeps `gradle.properties`, `init.d`, the wrapper's Gradle distributions,
and provisioned JDKs.
The default `maven-repository` rule deletes the contents of `~/.m2/repository`
and keeps `settings.xml` and the other files in `~/.m2`.
Maven has no command that prints the repository's path without a plugin,
so a repository moved by `localRepository` or `maven.repo.local`
is not covered; replace the rule with that `path` to use it.
Both rules need `java`, not `gradle` or `mvn`,
because many projects run these tools only through `gradlew` or `mvnw`.
A build that runs at the same time can fail.

A `delete_contents` rule can list `refuse_names`,
names of files or directories that hold data a download cannot restore:

```toml
refuse_names = ["maven-metadata-local.xml"]
```

Plonge skips the rule when its path holds an entry with one of these names
at any depth, and names the first one that it finds.
It also skips the rule when it cannot read a directory in the path,
which could hide one.
It checks when it lists the rule and again right before it deletes.
`mvn install` and `install:install-file` write `maven-metadata-local.xml`
for each artifact that they put in the repository,
and an artifact from `install-file` may exist nowhere else,
so `maven-repository` uses `refuse_names` and Plonge refuses that repository.
To clean it, delete the installed artifacts that you can build again,
or replace the rule without `refuse_names`.

A `delete_contents` rule must have kind `clear`,
so it runs only with `clean --global --include-clear --apply`.
Plonge makes read-only entries writable first and does not follow symlinks.
It cannot have `clean` or `targets`; it can have a `check`.
`refuse_names` is allowed only on a `delete_contents` rule.
The action reads `delete contents of <path> (built in)`.
Plonge skips the rule, and reports why, when the path is not absolute;
is `/`, the home directory, or a directory that contains the home directory,
compared by file identity so that a differently cased name cannot pass;
is a symlink or resolves through one to a different place;
is in a Git work tree or Git directory, or Git cannot check it,
as when the path or a directory above it has a `.git` entry that Git cannot use;
or does not exist or is not a directory.
It also skips the rule when the path holds a Git repository at any depth,
with the [same check as for project caches](#caches-that-hold-a-git-repository),
because the deletion would lose that repository's commits and uncommitted work.
It checks the path when it lists the rule and again right before it deletes.
`tool` must still be available, and a `path` command must still start with it.

The default `rubygems-cache` rule deletes the `cache` directory's contents
in the gem directory that `gem env gemdir` prints.
RubyGems keeps a copy of each `.gem` file that it installs there,
and has no command that removes the copies.
`gem cleanup` does something else: it uninstalls old gem versions,
which a project can still need, so Plonge does not run it.
After a clear, `gem pristine` downloads a missing copy.
`bundle cache` reports a missing gem file for a gem whose copy is gone;
with `--all-platforms`, it can download the gem instead.
The rule reaches only the Ruby that `gem` runs from your home directory;
each Ruby version has its own gem directory.

When your rule has the same `id` as a default, it replaces all of the default,
so copy the default's `check`, `require`, `refuse`, `checks`, and `targets`
to keep them.
For `mise-tools` with `tool = "mise"` and a `clean` command,
`path` is used only when mise cannot report its installs directory.
A `delete_contents` rule always uses its own `path`, or its `path_env`.

## Disk

`plonge disk` prints the free space on your home directory's volume.
`plonge clean --apply` also reports how much this free space changed,
as described in [Disk space](#disk-space).

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
names = [".astro", ".build", ".gradle", ".mypy_cache", ".next", ".nox",
         ".parcel-cache", ".pytest_cache", ".ruff_cache", ".svelte-kit", ".tox",
         ".turbo", ".venv", ".zig-cache", "DerivedData", "__pycache__", "_build",
         "bin", "build", "dist", "node_modules", "obj", "target", "tmp/cache",
         "vendor/bundle", "venv", "zig-cache"]
quiet_hours = 24

# Do not scan inside these directories.
skip = [".cache", ".git", ".hg", ".jj", ".svn"]

# Match these names only when a listed project file is in the same directory.
# A project file can be a pattern, such as "*.csproj".
# For a path name, such as "vendor/bundle", the project file is beside
# the first name in the path.
[beside]
".zig-cache" = ["build.zig"]
_build = ["mix.exs"]
bin = ["*.csproj", "*.fsproj", "*.vbproj"]
build = ["build.gradle", "build.gradle.kts", "pyproject.toml", "settings.gradle",
         "settings.gradle.kts", "setup.py"]
dist = ["package.json", "pyproject.toml", "setup.py"]
obj = ["*.csproj", "*.fsproj", "*.vbproj"]
target = ["Cargo.toml", "pom.xml"]
"tmp/cache" = ["config.ru"]
"vendor/bundle" = ["Gemfile", "gems.rb"]
venv = ["pyproject.toml", "requirements.txt", "setup.py"]
zig-cache = ["build.zig"]

# Refuse a match that has an entry at one of these paths, relative to it.
[refuse_inside]
_build = ["*/rel", "*/lib/*/src"]
```

To add rules, write them to `$XDG_CONFIG_HOME/plonge/rules.toml`
(by default, `~/.config/plonge/rules.toml`).
Your `names` and `skip` add to the defaults.
A `beside` entry in your file replaces the default entry for that name,
and a `quiet_hours` value replaces the default.
For example:

```toml
names = [".cache"]
quiet_hours = 72
skip = ["third_party"]

[beside]
build = ["build.gradle", "build.gradle.kts", "meson.build", "pyproject.toml",
         "settings.gradle", "settings.gradle.kts", "setup.py"]
```

This `build` entry adds Meson projects that use a `build` directory.
It replaces the default entry, so it lists the default project files too.

A `refuse_inside` entry lists paths, relative to a matching directory,
that hold data a rebuild cannot restore.
The default entry protects a release that Mix or Rebar3 builds
in `_build/<env or profile>/rel`,
which can hold a generated Erlang cookie and earlier release versions,
and the dependency sources that Rebar3 keeps in `_build/<profile>/lib`,
where an edit has no Git data that Plonge can check:

```toml
[refuse_inside]
_build = ["*/rel", "*/lib/*/src"]
```

Plonge refuses a match that has an entry, a file or a directory,
at one of these paths, and lists it with the path that it found:

```console
Refused ~/code/app/_build: contains prod/rel, which refuse_inside "*/rel" protects
```

Plonge checks when it measures the cache and again right before deletion.
Separate the elements of a path with `/`.
Each element is a name or a [`filepath.Match`](https://pkg.go.dev/path/filepath#Match) pattern,
and cannot be empty, `.`, or `..`, or contain `\`.
Plonge does not follow a symlink to a directory on the way to a path,
and it refuses the match when it cannot read a directory on the way.
A `refuse_inside` entry in your file replaces the default entry for that name.

A `beside` project file can be a pattern, such as `*.csproj`,
in the syntax of Go's [`filepath.Match`](https://pkg.go.dev/path/filepath#Match).
A name that contains `*`, `?`, `[`, or `\` is a pattern;
a pattern matches any entry in the same directory, as a literal name does.

A name in `names` can be a path of directory names, such as `vendor/bundle`.
It matches a directory named `bundle` whose parent directory is named `vendor`;
the `vendor` directory itself is not a cache.
The whole path must be below the scanned root.
For a path, a `beside` project file must be in the directory that holds
the first name, here the directory that holds `vendor`.
`beside` and `refuse_inside` entries for a path use the whole path as their key,
such as `"vendor/bundle" = ["keep"]`.

The scan never enters a directory in `skip`.
This keeps out caches that are nested in a tool's own cache,
such as `__pycache__` inside a `.cache/uv` environment.
A name in both `names` and `skip` is reported as a cache and is not entered.
`.git`, `.hg`, `.jj`, and `.svn` cannot be added to `names`,
alone or as part of a path.

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
