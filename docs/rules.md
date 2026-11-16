# Rule reference

See the [README](../README.md) for installation and a first run,
and [What Plonge covers](coverage.md) for tool coverage and known gaps.

## Project rules

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

## Project discovery and activity

Plonge reads mise's loaded config records from `$MISE_STATE_DIR/tracked-configs`,
`$XDG_STATE_HOME/mise/tracked-configs`, or `~/.local/state/mise/tracked-configs`.
It skips global and system configs and unreadable or broken links.
`--root` adds a root, and `--no-mise` uses only the roots you give;
`--no-mise` requires at least one `--root`.
Plonge skips a root outside a Git work tree,
but still scans separately listed roots inside it.
It removes duplicate roots and roots already covered by another Git work tree root.
Nested repositories use their own Git checks but share the root's activity status.
The scan does not follow directory symlinks or enter a matched cache.
Project cleanup can make read-only files and directories writable to remove them.

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

## Caches that hold a Git repository

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
The only exception is the clones that Swift Package Manager manages,
as [described below](#swift-package-manager-checkouts).
This protection also applies to worktrees with a `.git` file.
npm, pnpm, and Yarn 1 remove `.git` when installing packages from Git,
so those packages are not refused for that reason.

## Swift Package Manager checkouts

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

## Sizes and freed space

After `--apply`, the report gives two numbers:

```console
Estimated freed: 29.5 GiB (from cache sizes)
Free space on ~ grew by 14.0 GiB, from 80.1 GiB to 94.1 GiB; other processes and snapshots can change it
```

The estimate adds up the sizes of the caches that Plonge deleted or cleaned,
measured as this section describes.
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
as [Sizes and freed space](#sizes-and-freed-space) describes.
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
| `rubygems-cache` | clear | Built-in deletion of the contents of RubyGems' gem cache, except Bundler's clones of Git sources in `bundler` |
| `gradle-caches` | clear | Built-in deletion of the contents of `caches` in the Gradle user home |
| `maven-repository` | clear | Built-in deletion of the contents of `~/.m2/repository`, refused when it holds an artifact that `mvn install` put there |

Plonge runs a tool through `mise exec --`
when `mise which` finds it from your home directory,
and otherwise from `PATH`, passing over mise's shims.
It runs commands from your home directory, without a shell.
A rule whose tool is missing is skipped and reported.
Project-specific tool settings may therefore differ from those used for cleanup.
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

A stale directory usually holds the package clones that Xcode's SwiftPM keeps
in `SourcePackages`, which Xcode makes again when the workspace resolves.
Plonge allows them under the
[SwiftPM conditions](#swift-package-manager-checkouts),
except the checks against `Package.resolved`,
because the workspace that held the pins is gone:
a bare clone directly in `SourcePackages/repositories`
can mirror any origin,
and a checkout directly in `SourcePackages/checkouts`
can be at any commit that its `refs/remotes/origin` refs
or the tags that its bare clone also holds already hold.
A commit at the checkout's `HEAD` that no such ref holds is refused,
whether `HEAD` is detached or on a local branch.
Plonge refuses a stale directory that holds any other Git repository,
a clone that fails the other conditions, or a directory that it cannot read.
It checks again right before it deletes each directory,
and the outcome's size and action then count only the directories
that pass that check.
The report lists each refused directory and its reason
after the rule's action, and in `refused_folders` in JSON output;
its size is not in the rule's size, and it is not counted in the action:

```console
Refused ~/Library/Developer/Xcode/DerivedData/App-abc: contains a Git repository at SourcePackages/checkouts/dep that has changed, untracked, or ignored files
```

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
when a line has the label but no path,
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

A `delete_contents` rule can also list `keep_names`,
names of entries directly in its path that Plonge leaves in place:

```toml
keep_names = ["bundler"]
```

Plonge does not delete, measure, or check a kept entry,
so a Git repository inside it does not refuse the rule.
A name matches only directly in the path:
an entry with the same name deeper down is deleted and checked as usual.
The action reads `delete contents of <path> except <names> (built in)`.

A `delete_contents` rule must have kind `clear`,
so it runs only with `clean --global --include-clear --apply`.
Plonge makes read-only entries writable first and does not follow symlinks.
It cannot have `clean` or `targets`; it can have a `check`.
`refuse_names` and `keep_names` are allowed only on a `delete_contents` rule.
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
Bundler keeps a bare clone of each gem's Git source in `cache/bundler/git`,
and a clone can hold commits that its remote no longer has,
so the rule has `keep_names = ["bundler"]` and leaves `cache/bundler` in place.
`bundle clean` removes the clones that the current bundle does not use.
Plonge still refuses the rule when a Git repository is anywhere else in the cache.
The rule reaches only the Ruby that `gem` runs from your home directory;
each Ruby version has its own gem directory.

When your rule has the same `id` as a default, it replaces all of the default,
so copy the default's `check`, `require`, `refuse`, `checks`, and `targets`
to keep them.
For `mise-tools` with `tool = "mise"` and a `clean` command,
`path` is used only when mise cannot report its installs directory.
A `delete_contents` rule always uses its own `path`, or its `path_env`.
