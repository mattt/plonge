# What Plonge covers

Plonge cleans only the paths and actions selected by its rules.
Installing a tool with mise does not give Plonge a rule for all of its caches.
Later versions aim for one-to-one coverage of mise's core tools.

This table uses the default [rules](../internal/rules/default.toml),
the [project scanner](../internal/scan/detect.go),
and the [global cleanup code](../internal/global/global.go).
The built-in [Xcode rule](../internal/global/xcode.go) is included.
User rules can change this coverage.

The inventory was checked with mise **2026.10.14** on macOS ARM64,
using these commands:

```console
mise --version
mise registry -b core
mise backends ls
mise prune --help
mise cache prune --help
```

The registry returned 14 names for 13 core tools.
`dotnet-core` is an alias of `dotnet`; both resolve to `core:dotnet`.
The backend list returned the 20 names in the backend table below.
Backend cache locations were checked against
[mise's source at v2026.10.14][mise-source].
No cleanup command was applied to build this inventory.

The Bun, Deno, and Yarn 2+ rules were checked against the documentation
and source of [Bun 1.4.4][bun-pm], [Deno 2.9.8][deno-clean],
and [Yarn 4.18.1][yarn-clean],
the current stable releases when the rules were added.

The Zig rules were checked against the source of Zig **0.17.1**,
the current stable release.
Zig puts its local cache, `.zig-cache`, in the directory of `build.zig`
([source][zig-std]),
and `zig build` installs to `zig-out` there by default ([source][zig-maker]).
Zig 0.13 renamed the local cache from `zig-cache` ([release notes][zig-0.13]).
Zig has no command that cleans a cache ([usage][zig-main]),
so `zig-global-cache` uses the built-in deletion described below.
The global cache is `ZIG_GLOBAL_CACHE_DIR`, `$XDG_CACHE_HOME/zig`,
or `~/.cache/zig`, on macOS as well as Linux ([source][zig-std]).
`zig env` prints it as the `global_cache_dir` field of a ZON struct,
relative to the working directory when it is inside it ([source][zig-env]).
Plonge runs `zig env` from the home directory and reads that field.
`zig env` creates the directory if it does not exist.
The cache also holds the packages that `zig fetch` and `zig build` download,
so a clear downloads them again.

The .NET project rules follow the default output folders in Microsoft's
[`dotnet build` reference][dotnet-build] (`./bin/<configuration>/<framework>/`)
and [MSBuild common properties][msbuild-props] (`BaseIntermediateOutputPath`, `obj\`),
as published for .NET SDK 11.0.100.
The NuGet rules use the folder names and the `--list` and `--clear` options in
[`dotnet nuget locals`][dotnet-nuget-locals]
and [Managing the global packages and cache folders][nuget-folders].
In [NuGet.Client 8.2.0.14][nuget-locals-source], `--list` prints
each folder as `<name>: <path>`.
The macOS and Linux examples in the `dotnet nuget locals` reference
also show `info : ` before that text.
The rules set `path_label`, so Plonge reads the path in either form.
`--clear` deletes the folder's contents and does not check whether a project
uses them, so both rules are `clear`.
The default folders are `~/.nuget/packages` and `~/.local/share/NuGet/v3-cache`
on macOS and Linux;
`NUGET_PACKAGES`, `globalPackagesFolder`, and `NUGET_HTTP_CACHE_PATH` change them.

The tox and nox rules follow the defaults in their source.
In [tox 4.67.13][tox-work-dir], `work_dir` is `.tox` in the `tox_root` directory,
which is [the directory of the tox configuration file][tox-root] by default;
`--workdir` and the `work_dir` setting change it.
In [nox 2026.10.20][nox-envdir], `--envdir` is `.nox` by default,
and [nox changes to the noxfile's directory][nox-chdir] before it uses that path.
Both directories hold the environments that the tools create,
and the tools create them again on the next run.
Neither tool has a command that removes its environments,
so Plonge deletes the matching project directories.
Both names are specific to their tools, so they need no project file.

The Python `build` rule follows [setuptools 84.2.1][setuptools-build],
whose `build` command uses `build` in the project directory
as its `build_base` by default.
A [strict editable install][setuptools-editable] also puts its link tree
in that directory.
Setuptools has a [`clean --all`][setuptools-clean] command,
but it runs through the project's `setup.py`,
and it leaves the editable link tree and the `build` directory
when that tree is present.
Plonge's project rules delete directories and do not run project code,
so Plonge deletes the directory.
The next build writes it again; a strict editable install stops working
until it is installed again.
`build` is a common directory name, so the rule matches it only
beside `pyproject.toml` or `setup.py`, the files that start a setuptools build.
The Gradle rule below also matches `build`, beside Gradle's project files.
A `build` directory that matches is not entered,
so a cache inside it, such as `__pycache__`, is part of that match.

The Python `dist` rule follows the default output folders of the build frontends.
[build 1.6.1][pypa-build-outdir] writes to `dist` in the source directory,
[`uv build` in uv 0.13.8][uv-build-outdir] writes to `dist`
in the source directory or the workspace root,
and the `sdist` and `bdist` commands in [setuptools 84.2.1][setuptools-dist]
use `dist` by default.
The directory holds source distributions and wheels,
which a new build writes again.
[`uv publish`][uv-publish] uploads `dist/*` by default,
so a file that you built but did not upload must be built again.
Plonge matches `dist` beside `pyproject.toml` or `setup.py`,
in addition to `package.json`.

The Ruby project rules follow the source of Bundler **4.0.24**,
the current stable release, and Rails **8.1.4.1**.
Bundler installs gems in `vendor/bundle` when its `path` setting is `vendor/bundle`
or when `deployment` is set ([source][bundler-path]).
It expands a relative `path` from the directory of the Gemfile
([source][bundler-root]), which is named `gems.rb` or `Gemfile`
([source][bundler-gemfile]).
`bundle install` installs the gems there again.
So the rule matches `vendor/bundle` beside `Gemfile` or `gems.rb`,
and deletes the whole directory, as for `node_modules`.
A `vendor/bundle` directory with tracked files is not selected,
as for every project rule.
Bundler checks out a gem from a Git source with `git clone`
([source][bundler-git-clone]),
so a `vendor/bundle` that holds such a gem holds a Git repository,
and Plonge refuses it.

Plonge has no rule for Bundler's `vendor/cache`.
[`bundle cache`][bundle-cache] copies the `.gem` files of the bundle there on purpose,
so that `bundle install` can use them without a network,
and when the directory exists,
`bundle install` keeps it up to date ([source][bundler-install-cache]).
A committed `vendor/cache` has tracked files, so Plonge would not select it.
An ignored one can hold gems whose source is no longer available,
and deleting it stops `bundle install` from updating it.
The parent `vendor` directory is not a cache either:
it can hold `vendor/cache` and other vendored code.

Rails' default cache store is a file store in `tmp/cache`
([source][rails-cache-store]),
and [bootsnap 1.26.0][bootsnap-cache-dir] also puts its cache there
when the app's `config` directory sets it up and `BOOTSNAP_CACHE_DIR` is not set.
`bin/rails tmp:cache:clear` deletes what is in `tmp/cache`,
except names that start with a dot ([source][rails-tmp-rake]).
Plonge's project rules delete directories and do not run project code,
so Plonge deletes the directory, and Rails and bootsnap write it again.
Rails finds an application's root by `config.ru` ([source][rails-find-root]),
so the rule matches `tmp/cache` only beside `config.ru`.
New Rails apps ignore `tmp` except for `.keep` files ([source][rails-gitignore]),
so `tmp/cache` is ignored and has no tracked files.
The rest of `tmp` is left out:
it holds pid files, sockets, and, in development and test,
the secret key base in `tmp/local_secret.txt` ([source][rails-local-secret]),
which signs and encrypts cookies.

The `rubygems-cache` rule follows the source of RubyGems **4.0.24**.
When RubyGems installs a gem, it keeps a copy of the `.gem` file
in the `cache` directory of the gem directory ([source][rubygems-cache-file]).
`gem env gemdir` prints that gem directory ([source][rubygems-env]),
so the rule runs it from the home directory and adds `cache` with `path_subdir`.
RubyGems has no command that removes these copies,
so the rule uses the built-in deletion of the directory's contents.
[`gem cleanup`][rubygems-cleanup] is a different action:
it uninstalls old versions of installed gems,
which a project's `Gemfile.lock` can still need,
so Plonge does not run it and has no rule for it.
After a clear, [`gem pristine`][rubygems-pristine] downloads a copy that is missing.
Bundler looks for `.gem` files in `vendor/cache` and in this directory
([source][bundler-caches]),
and `bundle cache` stops with "Missing gem file" when neither has the file,
unless `--all-platforms` lets it download the gem ([source][bundler-cache-missing]).
The rule is `clear` because the copies belong to gems that are still installed.
Each Ruby version has its own gem directory,
and the rule reaches only the one of the Ruby that `gem` runs from the home directory.

The SwiftPM rules follow [Swift Package Manager 6.4.2][spm-purge].
`swift package purge-cache` removes the contents of the shared cache's
`repositories` and `registry/downloads` folders and its manifest cache
([source][spm-purge-caches]).
It does not check whether a project uses them, so both rules are `clear`;
the next resolve downloads them again.
It keeps the `artifacts` and `prebuilts` folders in the same cache
([source][spm-shared-folders]), which Plonge includes in the size,
so the size can be larger than the space that the command frees.
The shared cache is `org.swift.swiftpm` in the user's caches directory
([source][spm-cache-dir]),
which is [`~/Library/Caches`][apple-caches] on macOS
and `$XDG_CACHE_HOME`, or `~/.cache`, on Linux
([swift-foundation 6.4.2][foundation-xdg]).
Only the `--cache-path` option moves it ([source][spm-cache-path]),
and SwiftPM has no command that prints it,
so each rule measures the default path for its system
and passes the same path to `--cache-path`.
The command then cleans the cache that Plonge measured,
even when `XDG_CACHE_HOME` moves SwiftPM's own default;
a cache moved by `XDG_CACHE_HOME` or by another `--cache-path` is not covered.
This was checked with Swift 6.4.2 on macOS, in temporary directories.
Outside a package, SwiftPM writes `CACHEDIR.TAG` and a build-system file
to `.build` in its working directory
([scratch path][spm-scratch], [files][spm-scratch-files]).
Plonge runs commands from the home directory,
so the rules pass a `--scratch-path` relative to it
that names the cache directory, and SwiftPM does not create `~/.build`.
This was checked with Swift 6.4.2 on macOS, in a temporary directory.
`.swiftpm` in a package is not covered:
it holds the package's `mirrors.json` and `registries.json`
under `configuration` ([source][spm-local-config]),
and Xcode keeps the package's schemes and user data under `xcode`.
No part of it is a regenerable cache that a directory-name rule can match.

In a package's `.build`, SwiftPM 6.4.2 keeps a clone of each dependency
in `checkouts/<identity>`, with a `.git` directory and a detached `HEAD`
at the revision in `Package.resolved`, which borrows objects from
a bare mirror in `repositories/<name>-<hash>` through `alternates`.
`xcodebuild -resolvePackageDependencies -derivedDataPath DerivedData`
in a package makes the same layout in `DerivedData/SourcePackages`
and uses the `Package.resolved` beside `Package.swift`.
This was checked with Swift 6.4.2 and Xcode 27.2 on macOS,
with a dependency in a temporary Git repository;
`git status` in the checkout showed no files after `swift build`.
For an Xcode project with a remote package reference,
`xcodebuild -resolvePackageDependencies -project App.xcodeproj -scheme App -derivedDataPath DerivedData`
makes the same layout and writes the pins to
`App.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved`.
With `-workspace App.xcworkspace` for a workspace that holds the project,
it writes them to `App.xcworkspace/xcshareddata/swiftpm/Package.resolved`
and not to the project's file.
Without `-workspace` or `-project`, in a folder with that workspace and project,
it used the project and wrote the project's file,
and in a folder with two projects it stopped with an error.
This was checked with Xcode 27.2 on macOS, in a temporary directory.
Plonge lets these clones through only under the conditions in the
[rule reference](rules.md#swift-package-manager-checkouts).

The Mix and Rebar3 rules follow Elixir **1.20.5** and Rebar3 **3.28.0**,
the current stable releases.
Mix puts build output in `_build/<env>` beside `mix.exs` by default;
`MIX_BUILD_ROOT`, `MIX_BUILD_PATH`, and the `:build_path` option change it
([source][mix-build-path]).
An umbrella app's `mix.exs` sets `build_path` to the umbrella's `_build`
([source][mix-umbrella]), so the match is beside the umbrella's `mix.exs`.
Mix's `_build/<env>/lib/<app>` holds compiled output and symlinks
to each app's `include` and `priv` ([source][mix-build-structure]).
Plonge deletes those symlinks and not what they point to.
`mix clean --deps` removes the build output,
but it loads the project's `mix.exs` ([source][mix-clean]),
so Plonge deletes the directory, as it does for setuptools.
Rebar3 puts build output in `_build/<profile>` beside `rebar.config`,
and the sources of dependencies in `_build/<profile>/lib`
([source][rebar3-dirs], [defaults][rebar3-hrl]).
Rebar3 fetches a Git dependency with `git clone`, which keeps its `.git` directory
([source][rebar3-git]), so Plonge refuses a `_build` with a Git dependency.
An edit to a Hex dependency in `_build/<profile>/lib` has no such protection,
so Plonge does not match `_build` beside `rebar.config`,
and it refuses any `_build` that holds `*/lib/*/src`,
such as a Mix project that Rebar3 also built.
Rebar3 builds the dependencies in `_checkouts` in place of
fetched ones for local edits ([source][rebar3-checkouts]),
and Plonge does not match `_checkouts`.

Both tools build a release in `_build/<env or profile>/rel`
([Mix][mix-release-path], [Rebar3][rebar3-rel]).
When a Mix release has no `:cookie` option, Mix writes a random Erlang cookie
to its `releases/COOKIE` file and keeps it in later builds
([source][mix-cookie]); a new build writes a different one.
A Rebar3 release can also hold the earlier versions that `relup` needs.
So the `_build` rule has a `refuse_inside` path, `*/rel`,
and Plonge refuses a `_build` that holds a release.

Plonge does not match Mix's `deps` directory.
`deps` holds only dependency sources ([option][mix-deps-path]).
Mix keeps a Git dependency's `.git` directory ([source][mix-git]),
which Plonge refuses,
but Hex unpacks a package into `deps/<name>` with only a `.hex` manifest
([source][hex-unpack]).
An edit to such a package is not tracked by Git and has no `.git` entry,
so Plonge's checks cannot see it and a deletion would lose it.
`mix deps.clean --all` removes `deps` and its build output
([source][mix-deps-clean]), but Plonge does not run it.

Hex and Rebar3 have no command that cleans their package caches,
so `hex-packages` and `rebar3-hex` use the built-in deletion described below.
Hex 2.7.0 keeps package tarballs in `packages/<repo>` in its cache home
([source][hex-cache-path]).
The cache home is `HEX_HOME`, `$XDG_CACHE_HOME/hex` when `MIX_XDG` is set,
or `~/.hex` ([source][hex-home], [docs][hex-home-docs]).
`mix hex.config` does not print the packages folder,
so the rule uses the default, `~/.hex/packages`,
and a cache moved by `HEX_HOME` or `MIX_XDG` is not covered.
`~/.hex` also holds `hex.config`, with the user's Hex API key
([source][hex-config-path]), and the registry cache,
so the rule deletes only the contents of `packages`.
The next `mix deps.get` downloads a deleted tarball again;
`HEX_OFFLINE` uses only cached tarballs ([source][hex-offline]).
Rebar3 3.28.0 keeps the package index and tarballs in `hex`
in its global cache ([source][rebar3-hex-dir]),
which is `REBAR_CACHE_DIR` or `~/.cache/rebar3`
([source][rebar3-cache-dir], [override][rebar3-cache-env]).
Rebar3 has no command that prints it, so the rule uses `~/.cache/rebar3/hex`.
The global cache also holds global plugins
([source][rebar3-plugins]) and the `rebar3` that `rebar3 local install` writes
to `bin` and `vsns` ([source][rebar3-local-install]),
so the rule deletes only the contents of `hex`.
Both rules are `clear`, because they also delete packages that projects use.

## How to read the tables

The [README](../README.md#find-caches) explains roots, projects, and caches.
Its [cleanup guide](../README.md#delete-project-caches) explains activity selection.
For every row:

- `scan` reports matching project caches and deletes nothing.
  `scan --global` also lists global rules, including clear rules.
- `clean` shows a dry run for quiet projects.
  Add `--apply` to delete selected project caches after the path and Git checks.
  Busy projects are excluded; `--include-active` adds active projects.
  If `paseo` is on PATH but fails, every project is busy.
- `clean --global` shows global cleanup actions instead of project caches.
  Add `--apply` to run eligible prune actions.
  A prune rule's size marked `(whole cache)` is the size of its whole cache,
  of which the tool's policy removes only part.
  It does not use project activity or Git checks,
  except that a `delete_contents` rule refuses a path
  that is in or holds a Git repository.
- `--include-clear` requires `clean --global` and adds clear actions.
  It still deletes nothing without `--apply`.

Project cache names are exact directory names,
or exact paths of directory names such as `vendor/bundle`.
Every match must be inside a scanned root, ignored by Git,
and contain no tracked files.
Plonge refuses a match that is or holds a Git repository at any depth,
as the [rule reference](rules.md#caches-that-hold-a-git-repository) describes,
except for unchanged Swift Package Manager clones in `.build` or `DerivedData`
beside `Package.swift`, or in `DerivedData` beside one Xcode workspace or project
([rule reference](rules.md#swift-package-manager-checkouts)),
and a match that holds an entry at one of its rule's `refuse_inside` paths.
A listed project file must be in the cache directory's parent directory,
or for a path, in the directory that holds the path's first name.
A project file written as a pattern, such as `*.csproj`, matches any name of that form.
No project file is required unless the table names one.
The scan does not enter `.cache`, `.git`, `.hg`, `.jj`, or `.svn`,
or a directory that already matches a cache rule.
It does not follow directory symlinks.
These rules match names; they do not detect the project's language.

### Installed versions and shared mise caches

Every core tool and backend uses the same two mise rules below.
They do not clean language caches outside mise's directories.

| Rule ID | Path or contents measured | Command | Kind | Limits |
| --- | --- | --- | --- | --- |
| `mise-tools` | Versions listed by `mise ls --prunable --json` | `mise prune --tools --yes` | prune | Runs the guard below. Removes versions that mise considers unused, not every installed version. |
| `mise-cache` | Directory printed by `mise cache path` | `mise cache prune` | prune | Prunes by mise's cache-age settings. It can also prune an external task cache and the state-directory environment cache, which Plonge does not include in this size. |

The `mise-tools` guard runs `mise prune --tools --dry-run` during listing
and again before applying cleanup.
It skips the rule if that command fails or reports a tracked config
that cannot be loaded and may still exist.
It forces info-level logging so those warnings are visible.
The guard does not prove that every project has been recorded by mise.
See [mise's prune command][mise-prune] for how versions are selected,
including tracked configs, tool stubs, and running processes.

If the version list cannot be read, Plonge reports the size of
mise's installs directory and adds a note.
That directory is also used to estimate freed space.
It is `$MISE_INSTALLS_DIR`, or `installs` in the data directory
that `mise doctor --json` reports.
If mise cannot report it, Plonge uses `~/.local/share/mise/installs`
and adds a note.
Neither `scan` nor project `clean` selects installed versions by default.

In the core table, "mise rules" means these two rules, with this guard.
Global cleanup considers all available rules on the machine,
not just the rules associated with one table row.
The global-cache column lists additional rules relevant to that tool.

## Core tools

| Core name | Installed versions | Project caches | Global caches: ID, command, kind | Default behavior | Not covered |
| --- | --- | --- | --- | --- | --- |
| `bun` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` if used | `bun`: `bun pm cache prune` (clear; Bun 1.4.4 or later) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds `bun`, which removes package folders in Bun's cache more than 30 days old. | Packages newer than 30 days; the global store (`links/`), Git dependency clones, and package manifests, which `prune` keeps; Bun before 1.4.4, which the rule skips; a cache directory that only a `bunfig.toml`, `.npmrc`, or `.env` file names, which `prune` ignores. |
| `deno` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` if used | `deno-remote`, `deno-npm`, `deno-gen`: `delete_contents` of the `modulesCache`, `npmCache`, and `typescriptCache` paths from `deno info --json` (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds the three Deno rules. | `deno clean`, which also deletes user state; `location_data` (localStorage and Cache API data) and REPL history, which hold user state; the `registries` and `dl` folders and the analysis, check, and code caches in `DENO_DIR`. |
| `dotnet` | Guarded `mise-tools` | `bin` and `obj` beside `*.csproj`, `*.fsproj`, or `*.vbproj` | `nuget-packages`: `dotnet nuget locals global-packages --clear` (clear); `nuget-http-cache`: `dotnet nuget locals http-cache --clear` (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds both NuGet rules. | Output moved by `BaseOutputPath`, `BaseIntermediateOutputPath`, or `--output`; the [`artifacts` layout][dotnet-artifacts] (`UseArtifactsOutput`); `bin` and `obj` beside other project types; NuGet's `temp` and `plugins-cache` folders; `packages.config` folders set by `repositoryPath`; NuGet folders set only in a project's `nuget.config`. |
| `dotnet-core` (alias of `dotnet`) | Guarded `mise-tools`; same tool as `dotnet` | Same as `dotnet` | Same as `dotnet` | Same as `dotnet`. | Same as `dotnet`. |
| `elixir` | Guarded `mise-tools` | `_build` beside `mix.exs`, refused when it holds `*/rel` | `hex-packages`: `delete_contents` of `~/.hex/packages` (clear) | `scan` reports project matches; `clean` selects quiet ones. A `_build` that holds a release is refused. `clean --global` selects mise rules; `--include-clear` adds `hex-packages`. | Mix `deps`, which can hold edited dependency sources; `_build` moved by `MIX_BUILD_ROOT`, `MIX_BUILD_PATH`, or `:build_path`; `_build` with a release; a Hex cache moved by `HEX_HOME` or `MIX_XDG`; Hex's registry cache and docs. |
| `erlang` | Guarded `mise-tools` | none yet; `_build` beside `rebar.config` is left out because it holds dependency sources | `rebar3-hex`: `delete_contents` of `~/.cache/rebar3/hex` (clear) | `scan` and `clean` have no Rebar3 project rule. `clean --global` selects mise rules; `--include-clear` adds `rebar3-hex`. | Rebar3 `_build`, which holds dependency sources that a user can edit; `_checkouts`; a Rebar3 cache moved by `REBAR_CACHE_DIR`; Rebar3's global plugins and its `local install` copy. |
| `go` | Guarded `mise-tools` | none yet | `go-modules`: `go clean -modcache` (clear); `go-build`: `go clean -cache` (clear) | `scan` and `clean` have no Go project rule. `clean --global` selects mise rules; `--include-clear` adds both Go rules. | Project binaries, `vendor`, and caches at paths other than those returned by Go from the home directory. |
| `java` | Guarded `mise-tools` | `.gradle`; `target` beside `pom.xml`; `build` beside `build.gradle`, `build.gradle.kts`, `settings.gradle`, or `settings.gradle.kts` | `gradle-caches`: `delete_contents` of `caches` in the Gradle user home (clear); the user home is `$GRADLE_USER_HOME`, or `~/.gradle`. `maven-repository`: `delete_contents` of `~/.m2/repository` (clear), refused when it holds a `maven-metadata-local.xml` (`refuse_names`) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds `gradle-caches` and `maven-repository`. Both need `java`, not `gradle` or `mvn`, because many projects use only `gradlew` or `mvnw`. Gradle also deletes unused cache entries on its own. | A Maven repository that holds artifacts from `mvn install` or `install:install-file`, which the rule refuses; a Maven repository moved by `localRepository`, `maven.repo.local`, or Maven 4's `maven.user.conf`; Maven's `.mvn/target/project-local-repo`; a Gradle user home moved by `-g` or `gradle.user.home`, or a relative `GRADLE_USER_HOME`, which the rule skips; `wrapper/dists`, `jdks`, `daemon`, and `native` in the Gradle user home; `gradle.properties`, `init.d`, and the files in `~/.m2`, which hold configuration and credentials; `build` moved by `layout.buildDirectory`, or in a subproject with no build file of its own; `build.gradle.dcl` and `settings.gradle.dcl` projects; `.kotlin`. |
| `node` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` | `pnpm`: `pnpm store prune` (prune); `npm`: `npm cache clean --force` (clear); `yarn`: `yarn cache clean` (clear, Yarn 1); `yarn-berry`: `yarn cache clean` (clear, Yarn 2 and later) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and `pnpm`; `--include-clear` adds `npm` and the one Yarn rule that matches the version of `yarn` in the home directory. | Project `.yarn/cache` folders; the Yarn 2+ global cache when `yarn` in the home directory is Yarn 1 or `enableGlobalCache` is off; separate pnpm stores not resolved from the home directory; `build` output, unless a Python or Gradle project file is beside it. |
| `python` | Guarded `mise-tools` | `.venv`, `__pycache__`, `.mypy_cache`, `.pytest_cache`, `.ruff_cache`, `.tox`, `.nox`; `venv` beside `pyproject.toml`, `requirements.txt`, or `setup.py`; `build` and `dist` beside `pyproject.toml` or `setup.py` | `uv`: `uv cache prune` (prune); `pip`: `pip cache purge` (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and `uv`; `--include-clear` adds `pip`. | tox and nox environments moved by `--workdir`, `work_dir`, or `--envdir`; build output moved by setuptools' `build_base`; `*.egg-info`; `dist` moved by `--outdir` or `--out-dir`; uv-managed Python installations outside mise; caches at other configured paths. |
| `ruby` | Guarded `mise-tools` | `vendor/bundle` beside `Gemfile` or `gems.rb`; `tmp/cache` beside `config.ru` | `rubygems-cache`: `delete_contents` of `cache` in the directory that `gem env gemdir` prints (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds `rubygems-cache`. | Bundler's `vendor/cache`, which `bundle cache` fills on purpose; gems installed at a `path` other than `vendor/bundle`; a `vendor/bundle` that holds a gem from a Git source; the rest of a Rails app's `tmp`; caches moved by `cache_store` or `BOOTSNAP_CACHE_DIR`; gem caches of other Ruby versions and of `--user-install` gems; old gem versions, which `gem cleanup` uninstalls; Bundler's user cache (`~/.bundle/cache`); RubyGems' spec cache. |
| `rust` | Guarded `mise-tools` | `target` beside `Cargo.toml` | `cargo-registry`: `delete_contents` of `registry` in the Cargo home (clear); the Cargo home is `$CARGO_HOME`, or `~/.cargo` | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds `cargo-registry`. Cargo 1.88 and later also delete global cache files that they have not used for 1 or 3 months. | Cargo's Git cache (`git` in the Cargo home), whose checkouts are working trees that a user can edit; `cargo clean gc`, which is unstable; a relative `CARGO_HOME`, which the rules skip; a `CARGO_HOME` that only the environment of `mise exec` sets; `bin`, `config.toml`, `credentials.toml`, `.crates.toml`, and `.crates2.json`, which hold installed programs and user state; sccache, which has no cleanup command; toolchains managed outside mise. |
| `swift` | Guarded `mise-tools` | `.build`, `DerivedData` | `swiftpm-macos` (macOS): `swift package purge-cache --cache-path Library/Caches/org.swift.swiftpm --scratch-path Library/Caches/org.swift.swiftpm` (clear); `swiftpm-linux` (Linux): `swift package purge-cache --cache-path .cache/org.swift.swiftpm --scratch-path .cache/org.swift.swiftpm` (clear); `xcode-derived-data`: built-in deletion of stale Xcode build directories (prune, macOS) | `scan` reports project matches; `clean` selects quiet ones. `.build` and `DerivedData` beside `Package.swift`, and `DerivedData` beside one `.xcworkspace` or, without one, one `.xcodeproj`, whose `Package.resolved` is inside it, are allowed with remote dependencies when each bare clone mirrors a pinned `location` and each checkout is at its `Package.resolved` pin and has no local changes, stash, worktrees, pushes, or commits outside `HEAD`, its remote-tracking refs, and the tags that its bare clone holds; any other Git repository in them is refused, with its path. `clean --global` selects mise rules and the Xcode rule when available; `--include-clear` adds the SwiftPM rule for the system. | A `.build` or `DerivedData` whose checkouts have local changes or are not at their pins, or whose `Package.resolved` is missing or in the version 1 format; bare clones of a URL from SwiftPM's `mirrors.json`; a tag pushed from a checkout to its bare clone, or a commit pushed into a bare clone from outside the cache, which an offline check cannot detect; a bare clone whose origin URL differs from the pin's `location` in any way other than one trailing slash, a local path that does not resolve to the same directory, or a relative path, which is refused; `DerivedData` with package clones beside two or more workspaces, or beside no workspace and two or more projects; `DerivedData` beside a workspace and a project whose clones only the project's `Package.resolved` pins; SwiftPM's `artifacts` and `prebuilts` caches; a shared cache moved by `--cache-path` or `XDG_CACHE_HOME`; `.swiftpm`, which holds package configuration and Xcode schemes; Xcode build data for existing workspaces, archives, device support, and simulators. |
| `zig` | Guarded `mise-tools` | `.zig-cache` and `zig-cache` (before Zig 0.13) beside `build.zig` | `zig-global-cache`: `delete_contents` (built-in deletion) of the `global_cache_dir` that `zig env` prints (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds `zig-global-cache`. | `zig-out` install output, which can hold programs and libraries that a user runs; local caches moved with `--cache-dir` or `ZIG_LOCAL_CACHE_DIR`; the global cache when `zig env` prints JSON, as before Zig 0.15 (override the rule with a fixed path). |

"None yet" means there is no dedicated rule for that cache.
An unrelated directory name can still match a generic project rule.
Outside the shared mise rules, every core tool has a cache rule.

## Backends

A backend determines how mise installs a tool.
It does not determine where that tool later writes project or global caches.
All 20 backends have the shared `mise-tools` and `mise-cache` coverage above.

mise usually removes per-version download directories after installation.
If downloads remain, uninstalling a prunable version can remove its downloads
and version cache too, subject to mise's `always_keep_download` setting.
Plonge has no separate rule for all retained downloads or plugin directories.
See mise's [install and uninstall code][mise-backend].

| Backends | What can remain after installation | Rules that reach it | Not covered |
| --- | --- | --- | --- |
| `core` | Tool versions, version metadata, and retained toolchain downloads; language caches created later | Shared mise rules; language rules in the core table | The language gaps above; retained downloads outside the prune selection |
| `aqua`, `github`, `gitlab`, `forgejo`, `ubi`, `packslip` | Release metadata, verification data, and retained binary archives; `packslip` also caches policy data | `mise-cache` reaches data under mise's cache directory; `mise-tools` can remove per-version data when uninstalling | Retained downloads outside the prune selection; caches created later by installed tools unless another rule matches |
| `http` | Retained downloads and, with shared extraction enabled, extracted archives in mise's data directory under `http-tarballs` | Shared mise rules reach version caches and selected installs | Shared `http-tarballs` extractions have no rule; they are outside `mise-cache`'s scope ([source][mise-http]) |
| `s3` | Retained S3 downloads and extracted installs | Shared mise rules | Retained downloads outside the prune selection; caches created later by installed tools ([source][mise-s3]) |
| `asdf`, `vfox` | Plugin metadata, retained downloads, and any caches a plugin's install code creates | `mise-cache` reaches mise's cache directory; `mise-tools` delegates uninstall to the backend | Plugin directories and caches outside mise's cache directory have no general rule; plugin behavior varies |
| `cargo` | Cargo registry and Git downloads after source builds; optional compiler caches; prebuilt binaries when available | `mise-tools` removes selected installs; `cargo-registry` clears the Cargo home's registry cache; project `target` applies only in scanned roots beside `Cargo.toml` | Cargo's Git cache and sccache have no global rule ([backend][cargo]) |
| `go` | Go module downloads and build cache | `go-modules` and `go-build` clear the paths returned by `go env GOMODCACHE` and `go env GOCACHE` from the home directory | Caches at other configured paths |
| `npm` | Embedded installer cache and store under mise's cache directory, or external npm/pnpm/Bun caches when configured | `mise-cache` reaches the embedded cache; `npm` clears npm's cache; `pnpm` prunes its resolved store; `bun` prunes Bun's cache | Stores not resolved by the rules ([source][mise-npm]) |
| `pypi` | Per-tool environments and uv cache, or pip cache when installing through pipx | `mise-tools` removes selected environments; `uv` prunes and `pip` clears their resolved caches | Other pipx state and environments outside mise; caches at other configured paths ([backend][pypi]) |
| `gem` | Per-tool gems and gem archives under each install; separate RubyGems and Bundler caches | `mise-tools` removes selected installs as a whole; `rubygems-cache` clears the gem cache of the Ruby that `gem` runs from the home directory | No rule to prune archives within retained installs; gem caches of other Ruby versions; Bundler's user cache ([source][mise-gem]) |
| `dotnet` | .NET tool installs and NuGet package/HTTP caches | `mise-tools` removes selected installs; `nuget-packages` and `nuget-http-cache` clear the folders that `dotnet nuget locals` lists from the home directory | NuGet folders set for a single project; NuGet's `temp` and `plugins-cache` folders ([source][mise-dotnet]) |
| `conda` | Channel metadata under mise's cache directory; shared archives in its data directory under `conda-packages` | `mise-cache` reaches channel metadata; `mise-tools` removes selected environments | Shared `conda-packages` archives have no rule; external conda package caches have no rule ([source][mise-conda]) |
| `spinel` | Source checkout and compiler output in mise's download directory when retained | Shared mise rules reach version metadata and selected installs | Retained source/build downloads outside the prune selection; external compiler caches ([source][mise-spinel]) |
| `spm` | Shared package cache under mise's cache directory at `spm`; build output in the install; temporary source or artifact data if interrupted | `mise-cache` reaches the shared package cache; `mise-tools` removes selected installs | Swift Package Manager caches outside mise's cache directory have no rule ([source][mise-spm]) |

For every backend, plain `scan` and `clean` use only project-name rules.
Global backend cleanup requires `--global`;
clear rules also require `--include-clear`, and deletion requires `--apply`.
Plonge does not run the package manager for every installed tool version.
It resolves each rule's command from the home directory,
so a project-specific cache setting may not be used.

## Other tools covered by default

These tools can be installed independently of mise.
Project matches use the same Git and activity checks as the core table.
Global rules require their command to be available, except for the Xcode rule.

| Tool | Project cache names | Global rule ID | Command or action | Kind and default behavior |
| --- | --- | --- | --- | --- |
| Astro, Next.js, Parcel, SvelteKit, Turborepo | `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo`; `dist` beside `package.json` | none yet | Delete matching project directories | `scan` reports; `clean` selects quiet projects; `--apply` deletes. No dedicated global action. |
| mypy, pytest, Ruff | `.mypy_cache`, `.pytest_cache`, `.ruff_cache` | none yet | Delete matching project directories | `scan` reports; `clean` selects quiet projects; `--apply` deletes. |
| Mix, Rebar3 | `_build` beside `mix.exs` | none | Delete matching project directories; Plonge does not run `mix clean` or `rebar3 clean` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. A `_build` that holds a release (`*/rel`) or dependency sources (`*/lib/*/src`) is refused. Rebar3 `_build` beside `rebar.config` and Mix `deps` are not matched. |
| Hex, Rebar3 | none | `hex-packages`, `rebar3-hex` | `delete_contents` of `~/.hex/packages` and `~/.cache/rebar3/hex`; neither tool has a cleanup command | clear; requires `clean --global --include-clear --apply` to run. Caches moved by `HEX_HOME`, `MIX_XDG`, or `REBAR_CACHE_DIR` are not covered. |
| tox, nox | `.tox`, `.nox` | none | Delete matching project directories; neither tool has a cleanup command | `scan` reports; `clean` selects quiet projects; `--apply` deletes. Environments moved by `--workdir`, `work_dir`, or `--envdir` are not matched. |
| Maven, Gradle | `target` beside `pom.xml`; `.gradle`; `build` beside `build.gradle`, `build.gradle.kts`, `settings.gradle`, or `settings.gradle.kts` | `maven-repository`, `gradle-caches` | Delete matching project directories; `delete_contents` of `~/.m2/repository` and of `caches` in `$GRADLE_USER_HOME` or `~/.gradle`; neither tool has a command that cleans these caches | Project caches: `scan` reports; `clean` selects quiet projects; `--apply` deletes. Global rules: clear; require `clean --global --include-clear --apply` to run. `maven-repository` is refused when the repository holds a `maven-metadata-local.xml`. |
| setuptools | `build` beside `pyproject.toml` or `setup.py` | none | Delete matching project directories; Plonge does not run `setup.py clean --all` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. A `build_base` setting that moves the directory is not followed. |
| build, `uv build` | `dist` beside `pyproject.toml` or `setup.py` | none | Delete matching project directories; neither tool has a command that removes `dist` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. Output moved by `--outdir` or `--out-dir` is not matched. |
| Bundler | `vendor/bundle` beside `Gemfile` or `gems.rb` | none | Delete matching project directories; Plonge does not run `bundle clean` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. `vendor/cache` and gems at another `path` are not matched. |
| Rails, bootsnap | `tmp/cache` beside `config.ru` | none | Delete matching project directories; Plonge does not run `bin/rails tmp:cache:clear` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. The rest of `tmp` is not matched. |
| uv | `.venv`; `venv` beside `pyproject.toml`, `requirements.txt`, or `setup.py` | `uv` | `uv cache prune` | prune; eligible with `clean --global`; requires `--apply` to run |
| pip | The same Python environment names | `pip` | `pip cache purge` | clear; requires `clean --global --include-clear --apply` to run |
| npm | `node_modules` | `npm` | `npm cache clean --force` | clear; requires `clean --global --include-clear --apply` to run |
| pnpm | `node_modules` | `pnpm` | `pnpm store prune` | prune; eligible with `clean --global`; requires `--apply` to run |
| Bun | `node_modules` | `bun` | `bun pm cache prune` | clear; requires `clean --global --include-clear --apply` to run. Bun 1.4.4 or later. |
| Deno | `node_modules` | `deno-remote`, `deno-npm`, `deno-gen` | `delete_contents` of the paths that `deno info --json` prints | clear; requires `clean --global --include-clear --apply` to run. |
| Yarn 1 | `node_modules` | `yarn` | `yarn cache clean` | clear; requires `clean --global --include-clear --apply` to run. Only when `yarn --version` reports 1.x. |
| Yarn 2 and later | `node_modules` | `yarn-berry` | `yarn cache clean` | clear; requires `clean --global --include-clear --apply` to run. Only when `yarn --version` reports 2.x or later. |
| Cargo | `target` beside `Cargo.toml` | `cargo-registry` | `delete_contents` of `registry` in `$CARGO_HOME`, or `~/.cargo` | clear; requires `clean --global --include-clear --apply` to run. `git`, `bin`, configuration, and credentials remain. |
| sccache | none | none | none; sccache 0.19.0 has no command that clears or prunes its cache | Not covered. |
| RubyGems | none | `rubygems-cache` | `delete_contents` of `cache` in the directory that `gem env gemdir` prints; Plonge does not run `gem cleanup`, which uninstalls old gem versions | clear; requires `clean --global --include-clear --apply` to run. Installed gems stay; only the copies of their `.gem` files are deleted. |
| Homebrew | none yet | `homebrew` | `brew cleanup --prune=all` | clear; requires `clean --global --include-clear --apply` to run. Homebrew can remove old installed versions as well as cached downloads; Plonge measures only `brew --cache`. |
| Swift Package Manager | `.build` | `swiftpm-macos`, `swiftpm-linux` | `swift package purge-cache`, with `--cache-path` and `--scratch-path` set to the measured cache directory | clear; requires `clean --global --include-clear --apply` to run. Each rule applies only on its system. `artifacts` and `prebuilts` remain. |
| Xcode | `DerivedData` | `xcode-derived-data` | Built-in deletion of stale directories under `~/Library/Developer/Xcode/DerivedData`; no external command | prune; macOS only, when that directory can be read. Eligible with `clean --global`; requires `--apply` to delete. Refuses a stale directory that holds a Git repository, except unchanged SwiftPM clones in `SourcePackages`, or a directory that it cannot read. |

A global rule can also set `delete_contents` in place of a cleanup command,
for a cache whose tool has none.
Plonge then deletes the contents of the rule's path and keeps the directory.
Such a rule must be a clear rule, so it runs only with
`clean --global --include-clear --apply`.
Plonge skips it when the path is not absolute; is `/`, the home directory,
or contains the home directory; is or resolves through a symlink;
is in a Git repository or holds one at any depth; or does not exist.
The default `zig-global-cache`, `rubygems-cache`, `hex-packages`, `rebar3-hex`,
`gradle-caches`, and `maven-repository` rules,
the Deno rules, and the Cargo rules use `delete_contents`.
A `delete_contents` rule can also list `refuse_names`:
Plonge skips the rule when its path holds an entry with one of those names
at any depth, or a directory that it cannot read.
See the [rule reference](rules.md#global-caches) for the rule format.

The Xcode global rule selects only directories whose XML `info.plist`
names an absolute `WorkspacePath` that no longer exists.
It keeps directories with missing or unreadable workspace metadata
and workspaces on unmounted volumes under `/Volumes`.
It also refuses a stale directory that holds a Git repository at any depth,
or a directory that it cannot read,
except the SwiftPM clones in `SourcePackages` that pass the project checks
for `.build`, other than the checks against `Package.resolved`,
which is gone with the workspace.
Without the pins, Plonge cannot tell whether a checkout was at the commit
that the workspace resolved, or whether a bare clone mirrors the package's URL;
it can tell only that neither holds work that Git cannot restore,
so a checkout's `HEAD` must be a commit that its remote-tracking refs
or its bare clone's tags hold.
The project `DerivedData` rule is different: it uses the project checks,
without checking Xcode workspace metadata.

### Cargo

The Cargo rules were checked against the source of Cargo in Rust **1.100.0**,
the current stable release
([Cargo commit 3acc79c][cargo-1.100]).
Cargo 1.88 and later clean the global cache automatically,
at most once a day by default (`cache.auto-clean-frequency`),
during commands such as `cargo build` and not when Cargo runs offline.
They delete downloaded files that they have not used for 3 months,
and extracted sources that they have not used for 1 month
([source][cargo-gc]).
The command that cleans the cache on request, `cargo clean gc`,
is still unstable:
it fails on the stable channel unless `-Z gc` enables it
([source][cargo-clean-gc]).
Plonge does not use unstable flags,
so `cargo-registry` deletes the contents
of the `registry` folder in the Cargo home.
It holds the crate index and downloaded `.crate` files and their sources
([Cargo home][cargo-home-doc]).
The next build downloads what it needs again,
so the rule is `clear`.
The `git` folder holds Git dependencies as bare repositories and checkouts;
a checkout is a working tree that a user can edit, so Plonge leaves `git` out.
The other files in the Cargo home stay:
`bin` holds programs that `cargo install` and rustup installed,
`config.toml` and `credentials.toml` hold settings and login tokens,
and `.crates.toml` and `.crates2.json` record installed crates.

Cargo has no stable command that prints the Cargo home.
Cargo uses `CARGO_HOME` when it is set and not empty,
and otherwise `.cargo` in the home directory
([source][cargo-home-env]).
The rule follows the same order with `path_env = "CARGO_HOME"`,
and `path_subdir = "registry"` selects `registry` inside the Cargo home.
Cargo resolves a relative `CARGO_HOME` from its working directory,
which Plonge cannot know for every command, so Plonge skips the rule
when `CARGO_HOME` is not an absolute path.
Plonge reads `CARGO_HOME` from its own environment.
If `CARGO_HOME` is set only in the environment that `mise exec` gives `cargo`,
Plonge uses `~/.cargo`.
Deletion keeps the `registry` folder itself,
and its contents get the same checks as other `delete_contents` rules;
the `git` folder is not touched.
A Cargo build that runs at the same time can fail,
because Cargo can read a file that Plonge then deletes.

sccache keeps its local cache in `SCCACHE_DIR`,
or by default in `~/Library/Caches/Mozilla.sccache` on macOS
and `~/.cache/sccache` on Linux ([sccache 0.19.0][sccache-local]).
Its options in sccache 0.19.0 start and stop the server and show
or reset statistics; none clears or prunes the cache
([source][sccache-cmdline]).
sccache limits the cache to 10 gigabytes by default.
Plonge has no sccache rule.

### Gradle

The Gradle rules were checked against the documentation and source
of Gradle **9.9.1**, the current stable release.
Gradle recognizes `build` and `settings` files with the extensions
`.gradle`, `.gradle.kts`, and `.gradle.dcl`
([names][gradle-logic-files], [extensions][gradle-script-langs]).
A project's output goes in `build` in the project directory by default
([source][gradle-build-dir]), and `layout.buildDirectory` can move it
([source][gradle-layout]).
`clean` deletes that directory ([base plugin][gradle-base-plugin]),
so a later build writes it again.
Plonge matches `build` only beside `build.gradle`, `build.gradle.kts`,
`settings.gradle`, or `settings.gradle.kts`.
A subproject that has no build file of its own, because the root build
configures it, is not matched.
`.gradle.dcl` files are for Gradle's declarative preview
and are not matched yet.
`buildSrc` is a separate build with its own build file
([docs][gradle-buildsrc]), so its `build` matches in the same way.
The project `.gradle` directory holds Gradle's incremental build state and
the configuration cache ([docs][gradle-project-cache]);
Plonge already matched it by name.

Gradle has no command that cleans the caches in its user home
([command line][gradle-cli]).
It deletes cache entries that it has not used for 7 to 30 days on its own,
when a daemon stops, unless an init script disables cleanup
([docs][gradle-cleanup], [retention][gradle-retention]).
`gradle-caches` deletes the contents of `caches` in the user home:
the dependency cache, transforms, the local build cache,
and the caches of each Gradle version ([docs][gradle-caches-doc]).
Later builds download and make them again, so the rule is `clear`.
`caches` also holds `user-id.txt`, a persistent ID
([source][gradle-user-id]); Gradle writes a new one when it is missing.
The rest of the user home stays:
`gradle.properties` holds settings and can hold credentials
([docs][gradle-user-home-secure]), `init.d` holds init scripts,
and `wrapper/dists`, `jdks`, `daemon`, and `native`
hold Gradle distributions, provisioned JDKs, daemon logs, and native libraries
([layout][gradle-user-home]).

Gradle has no command that prints its user home;
`gradle --version` does not show it ([source][gradle-version-info]).
Gradle uses the `gradle.user.home` system property,
then `GRADLE_USER_HOME`, then `.gradle` in the home directory,
and `-g` overrides all three
([source][gradle-user-home-lookup], [CLI][gradle-user-home-cli]).
The rule follows `GRADLE_USER_HOME` with `path_env` and `path_subdir = "caches"`,
and skips the rule when it is not an absolute path.
A user home moved by `-g` or `gradle.user.home`,
in `GRADLE_OPTS` or a project's `gradle.properties`, is not covered.
Gradle locks its caches for concurrent use
([docs][gradle-cache-locking]), but Plonge does not take those locks,
so a build or daemon that runs at the same time can fail;
`gradle --stop` stops the daemons first.

The rule needs `java` and not `gradle`,
because many projects run Gradle only through `gradlew`,
and every Gradle build runs on Java.
Plonge does not run `java`.

### Maven

The Maven rule was checked against the source of Maven **3.10.0**,
the current stable release, and of Maven Resolver **2.0.24**, which it uses.
Maven 4 is in preview; it was checked against **4.0.0-rc-7**.
Maven has no command that cleans the local repository.
[`dependency:purge-local-repository`][maven-purge] removes the dependencies
of one project, and downloads them again by default.
So `maven-repository` deletes the contents of the local repository.
Later builds download what they need again, so the rule is `clear`.
The files beside it in `~/.m2` stay:
`settings.xml`, `settings-security.xml`, `toolchains.xml`, and `extensions.xml`
hold settings, credentials, and the master password
([source][maven-user-files], [security][maven-security]).

The local repository is `~/.m2/repository` by default
([source][maven-default-repo]).
`<localRepository>` in the user or global `settings.xml`
([merge][maven-settings-merge]),
`maven.repo.local` in `MAVEN_OPTS`, `MAVEN_ARGS`, or a project's
`.mvn/maven.config` ([source][maven-repo-local]),
and, in Maven 4, `maven.user.conf` and the `maven-user.properties` files
([constants][maven4-constants], [order][maven4-repo-order]) move it.
Maven prints the path only in a build's debug output,
and `help:evaluate` needs a plugin that Maven can have to download
([help plugin][maven-help-evaluate]),
so the rule uses the default path.
A moved repository is not covered.

`mvn install` and `install:install-file` copy artifacts into the repository
([install plugin][maven-install-file], [installer][resolver-installer]).
An artifact from `install-file` may exist in no remote repository,
so deleting it can lose it.
Maven Resolver writes metadata for an installed artifact
to `maven-metadata-local.xml`
([path][resolver-local-metadata], [name][resolver-metadata-name],
[generator][maven-versions-metadata]),
and downloads write `maven-metadata-<repository>.xml` instead
([source][resolver-remote-metadata]).
So the rule has `refuse_names = ["maven-metadata-local.xml"]`,
and Plonge refuses the whole repository when it holds one, at any depth,
and lists the first one that it finds:

```console
Skipped maven-repository: contains com/example/lib/maven-metadata-local.xml, which refuse_names "maven-metadata-local.xml" protects
```

To use the rule, delete the installed artifacts that you can build again,
or replace the rule without `refuse_names`.
A remote repository whose ID is `local` gives the same file name,
so its downloads also make Plonge refuse.
Maven Resolver keeps its locks in `.locks` in the repository
([source][resolver-locks]), so a build that runs at the same time can fail.

The rule needs `java` and not `mvn`,
because many projects run Maven only through `mvnw`.
Plonge does not run `java`.

### Bun

The `bun` rule asks `bun pm cache` for the cache path
and runs `bun pm cache prune`.
That command removes package folders that Bun has neither extracted nor,
where the file system records access times, linked from in 30 days.
macOS and `noatime` mounts do not record those access times,
so a package that projects still install every day can count as old.
For this reason the rule has kind clear.
The next install downloads a removed package again.
`prune` keeps the global store in `links/`,
which projects with `globalStore = true` link to,
and Git dependency clones, package manifests, and transpiler output.
Plonge does not use `bun pm cache rm`,
because it also deletes the global store and so breaks those projects
until each one runs `bun install` again.

Bun added `prune` in 1.4.4,
so the rule's check runs `bun --version`
and skips the rule for an earlier version,
or when it prints no version line or more than one.
Bun 1.3 prints its cache path only inside a project with a `package.json`,
so for Bun 1.3 the rule is skipped earlier, because its path command fails.
`prune` takes its directory from `BUN_INSTALL_CACHE_DIR`, `BUN_INSTALL`,
or `~/.bunfig.toml`, and ignores a directory named in another
`bunfig.toml`, `.npmrc`, or `.env` file.
See [Bun's `pm cache` documentation][bun-pm] and [prune code][bun-prune].

### Deno

Deno's own command, `deno clean`, deletes all of `DENO_DIR`.
That directory also holds user state:
localStorage and Cache API data under `location_data`,
and the REPL history in `deno_history.txt` unless `DENO_REPL_HISTORY` moves it.
Plonge does not run `deno clean`.
Its three Deno rules each read one path from `deno info --json`,
so they follow `DENO_DIR`, and delete the contents of that path:
`deno-remote` for remote modules (`remote`),
`deno-npm` for npm packages (`npm`),
and `deno-gen` for compiled TypeScript (`gen`).
The next run downloads or compiles them again.
The other folders and files in `DENO_DIR` stay.
See Deno's [clean][deno-clean], [info][deno-info],
and [`DENO_DIR` layout][deno-dir] code.

### Yarn

The `yarn` and `yarn-berry` rules run the same `yarn`,
resolved from the home directory.
Each rule's check runs `yarn --version`
and requires exactly one version line in its output.
Each rule then refuses the versions that the other accepts:
`yarn` runs only for 1.x, and `yarn-berry` only for 2.x or later.
So at most one Yarn rule acts on an installation.
If the output has no version line, or more than one, both rules are skipped.

For Yarn 2 and later, `yarn-berry` asks `yarn config get cacheFolder`
for the path and runs `yarn cache clean`, which deletes that folder.
With `enableGlobalCache`, which is on by default in Yarn 4.18.1,
`cacheFolder` is the `cache` folder in `globalFolder`:
by default `~/.yarn/berry/cache`,
or `$XDG_DATA_HOME/yarn/berry/cache` when `XDG_DATA_HOME` is set.
With `enableGlobalCache` off, `cacheFolder` is a project's `.yarn/cache`,
which a project with zero-installs keeps in Git.
A command rule has no Git check, and the home directory can be,
or be inside, a Yarn project.
So `yarn-berry` has a second check, `yarn config get enableGlobalCache`,
and runs only when Yarn prints exactly one line, `true`.
Plonge runs both checks when it lists the rule
and again right before cleanup,
and skips the rule when the setting is off or Yarn cannot report it.
If `enableCacheClean` is off, `yarn cache clean` fails,
and Plonge reports the failure.
`yarn` in the home directory can be Yarn 1
even when projects choose a later version, for example in `packageManager`.
Then only the `yarn` rule applies, and the Yarn 2+ global cache stays.
See Yarn's [cache clean command][yarn-clean] and
[configuration][yarn-config] code.

[mix-build-path]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/project.ex#L782-L786
[mix-umbrella]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/project.ex#L140-L148
[mix-build-structure]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/project.ex#L937-L961
[mix-clean]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/tasks/clean.ex#L31-L40
[mix-deps-path]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/project.ex#L68-L69
[mix-git]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/scm/git.ex#L105-L111
[mix-deps-clean]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/tasks/deps.clean.ex#L8-L20
[mix-release-path]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/release.ex#L128-L132
[mix-cookie]: https://github.com/elixir-lang/elixir/blob/v1.20.5/lib/mix/lib/mix/release.ex#L570-L586
[hex-unpack]: https://github.com/hexpm/hex/blob/v2.7.0/lib/hex/scm.ex#L178-L211
[hex-cache-path]: https://github.com/hexpm/hex/blob/v2.7.0/lib/hex/scm.ex#L449-L453
[hex-home]: https://github.com/hexpm/hex/blob/v2.7.0/lib/hex/config.ex#L163-L174
[hex-home-docs]: https://github.com/hexpm/hex/blob/v2.7.0/lib/mix/tasks/hex.config.ex#L123-L129
[hex-config-path]: https://github.com/hexpm/hex/blob/v2.7.0/lib/mix/tasks/hex.config.ex#L281-L285
[hex-offline]: https://github.com/hexpm/hex/blob/v2.7.0/lib/hex/scm.ex#L455-L469
[rebar3-hex-dir]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_packages.erl#L158-L175
[rebar3-cache-dir]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_dir.erl#L168-L172
[rebar3-cache-env]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar3.erl#L175-L180
[rebar3-plugins]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_prv_plugins.erl#L39
[rebar3-local-install]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_prv_local_install.erl#L82-L94
[rebar3-dirs]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_dir.erl#L40-L79
[rebar3-hrl]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar.hrl#L15-L25
[rebar3-git]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_git_resource.erl#L191-L196
[rebar3-checkouts]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_prv_install_deps.erl#L330-L336
[rebar3-rel]: https://github.com/erlang/rebar3/blob/3.28.0/apps/rebar/src/rebar_relx.erl#L36
[mise-source]: https://github.com/jdx/mise/tree/v2026.10.14/src/backend
[mise-prune]: https://github.com/jdx/mise/blob/v2026.10.14/src/cli/prune.rs
[mise-backend]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/mod.rs
[mise-http]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/http.rs
[mise-s3]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/s3.rs
[mise-npm]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/npm.rs
[mise-gem]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/gem.rs
[mise-dotnet]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/dotnet.rs
[mise-conda]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/conda.rs
[mise-spinel]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/spinel.rs
[mise-spm]: https://github.com/jdx/mise/blob/v2026.10.14/src/backend/spm.rs
[bun-pm]: https://github.com/oven-sh/bun/blob/bun-v1.4.4/docs/pm/cli/pm.mdx#cache
[bun-prune]: https://github.com/oven-sh/bun/blob/bun-v1.4.4/src/runtime/cli/pm_cache_prune_command.rs
[cargo]: https://mise.jdx.dev/dev-tools/backends/cargo.html
[cargo-1.100]: https://github.com/rust-lang/rust/tree/1.100.0/src/tools
[cargo-gc]: https://github.com/rust-lang/cargo/blob/3acc79c0c841844675bc53a605922dbfc441e750/src/workspace/gc.rs#L31-L38
[cargo-clean-gc]: https://github.com/rust-lang/cargo/blob/3acc79c0c841844675bc53a605922dbfc441e750/src/bin/cargo/commands/clean.rs#L176-L183
[cargo-home-doc]: https://github.com/rust-lang/cargo/blob/3acc79c0c841844675bc53a605922dbfc441e750/doc/book/src/guide/cargo-home.md
[cargo-home-env]: https://github.com/rust-lang/cargo/blob/3acc79c0c841844675bc53a605922dbfc441e750/crates/home/src/env.rs#L67-L81
[sccache-local]: https://github.com/mozilla/sccache/blob/v0.19.0/docs/Local.md
[sccache-cmdline]: https://github.com/mozilla/sccache/blob/v0.19.0/src/cmdline.rs#L129-L160
[pypi]: https://mise.jdx.dev/dev-tools/backends/pypi.html
[deno-clean]: https://github.com/denoland/deno/blob/v2.9.8/cli/tools/clean.rs
[deno-info]: https://github.com/denoland/deno/blob/v2.9.8/cli/tools/info.rs
[deno-dir]: https://github.com/denoland/deno/blob/v2.9.8/libs/resolver/cache/deno_dir.rs
[yarn-clean]: https://github.com/yarnpkg/berry/blob/%40yarnpkg/cli/4.18.1/packages/plugin-essentials/sources/commands/cache/clean.ts
[yarn-config]: https://github.com/yarnpkg/berry/blob/%40yarnpkg/cli/4.18.1/packages/yarnpkg-core/sources/Configuration.ts
[zig-std]: https://codeberg.org/ziglang/zig/src/tag/0.17.1/lib/std/zig.zig
[zig-maker]: https://codeberg.org/ziglang/zig/src/tag/0.17.1/lib/compiler/Maker.zig
[zig-0.13]: https://ziglang.org/download/0.13.0/release-notes.html#codezig-cachecode-renamed-to-codezig-cachecode
[zig-env]: https://codeberg.org/ziglang/zig/src/tag/0.17.1/src/print_env.zig
[zig-main]: https://codeberg.org/ziglang/zig/src/tag/0.17.1/src/main.zig
[dotnet-build]: https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-build
[msbuild-props]: https://learn.microsoft.com/en-us/visualstudio/msbuild/common-msbuild-project-properties
[dotnet-artifacts]: https://learn.microsoft.com/en-us/dotnet/core/sdk/artifacts-output
[dotnet-nuget-locals]: https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-nuget-locals
[nuget-folders]: https://learn.microsoft.com/en-us/nuget/consume-packages/managing-the-global-packages-and-cache-folders
[tox-work-dir]: https://github.com/tox-dev/tox/blob/4.67.13/src/tox/config/sets.py#L311-L312
[tox-root]: https://github.com/tox-dev/tox/blob/4.67.13/src/tox/config/sets.py#L328-L333
[nox-envdir]: https://github.com/wntrblm/nox/blob/2026.10.20/nox/_options.py#L212-L221
[nox-chdir]: https://github.com/wntrblm/nox/blob/2026.10.20/nox/tasks.py#L119-L138
[setuptools-build]: https://github.com/pypa/setuptools/blob/v84.2.1/setuptools/_distutils/command/build.py#L54
[setuptools-editable]: https://github.com/pypa/setuptools/blob/v84.2.1/setuptools/command/editable_wheel.py#L373-L379
[pypa-build-outdir]: https://github.com/pypa/build/blob/1.6.1/src/build/__main__.py#L742-L747
[uv-build-outdir]: https://github.com/astral-sh/uv/blob/0.13.8/crates/uv-build-commands/src/lib.rs#L402-L413
[setuptools-clean]: https://github.com/pypa/setuptools/blob/v84.2.1/setuptools/_distutils/command/clean.py#L56-L78
[setuptools-dist]: https://github.com/pypa/setuptools/blob/v84.2.1/setuptools/_distutils/command/sdist.py#L174
[uv-publish]: https://github.com/astral-sh/uv/blob/0.13.8/crates/uv-cli/src/lib.rs#L7433-L7438
[nuget-locals-source]: https://github.com/NuGet/NuGet.Client/blob/8.2.0.14/src/NuGet.Core/NuGet.Commands/CommandRunners/LocalsCommandRunner.cs#L109-L117
[bundler-path]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/settings.rb#L237-L251
[bundler-root]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler.rb#L101-L103
[bundler-gemfile]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/shared_helpers.rb#L269-L271
[bundler-git-clone]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/source/git/git_proxy.rb#L137-L146
[bundle-cache]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/man/bundle-cache.1.ronn
[bundler-install-cache]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/cli/install.rb#L53
[rails-cache-store]: https://github.com/rails/rails/blob/v8.1.4.1/railties/lib/rails/application/configuration.rb#L58
[bootsnap-cache-dir]: https://github.com/Shopify/bootsnap/blob/v1.26.0/lib/bootsnap.rb#L125-L143
[rails-tmp-rake]: https://github.com/rails/rails/blob/v8.1.4.1/railties/lib/rails/tasks/tmp.rake#L17-L22
[rails-find-root]: https://github.com/rails/rails/blob/v8.1.4.1/railties/lib/rails/application.rb#L86-L88
[rails-gitignore]: https://github.com/rails/rails/blob/v8.1.4.1/railties/lib/rails/generators/rails/app/templates/gitignore.tt#L13-L23
[rails-local-secret]: https://github.com/rails/rails/blob/v8.1.4.1/railties/lib/rails/application/configuration.rb#L653-L654
[rubygems-cache-file]: https://github.com/rubygems/rubygems/blob/v4.0.24/lib/rubygems/installer.rb#L926-L931
[rubygems-env]: https://github.com/rubygems/rubygems/blob/v4.0.24/lib/rubygems/commands/environment_command.rb#L82-L83
[rubygems-cleanup]: https://github.com/rubygems/rubygems/blob/v4.0.24/lib/rubygems/commands/cleanup_command.rb#L8-L12
[rubygems-pristine]: https://github.com/rubygems/rubygems/blob/v4.0.24/lib/rubygems/commands/pristine_command.rb#L169-L188
[bundler-caches]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/source/rubygems.rb#L36-L38
[bundler-cache-missing]: https://github.com/rubygems/rubygems/blob/v4.0.24/bundler/lib/bundler/source/rubygems.rb#L233-L235
[spm-purge]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/Commands/PackageCommands/ResetCommands.swift#L32-L44
[spm-purge-caches]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/CoreCommands/SwiftCommandState.swift#L643-L688
[spm-shared-folders]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/Workspace/Workspace+Configuration.swift#L142-L161
[spm-cache-dir]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/Basics/FileSystem/FileSystem+Extensions.swift#L245-L254
[spm-cache-path]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/CoreCommands/SwiftCommandState.swift#L1391-L1402
[spm-scratch]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/CoreCommands/SwiftCommandState.swift#L463-L466
[spm-scratch-files]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/CoreCommands/SwiftCommandState.swift#L215-L238
[spm-local-config]: https://github.com/swiftlang/swift-package-manager/blob/swift-6.4.2-RELEASE/Sources/Workspace/Workspace+Configuration.swift#L227-L250
[apple-caches]: https://developer.apple.com/documentation/foundation/filemanager/searchpathdirectory/cachesdirectory
[foundation-xdg]: https://github.com/swiftlang/swift-foundation/blob/swift-6.4.2-RELEASE/Sources/FoundationEssentials/FileManager/SearchPaths/FileManager+XDGSearchPaths.swift#L45-L52
[gradle-logic-files]: https://github.com/gradle/gradle/blob/v9.9.1/platforms/core-runtime/build-discovery/src/main/java/org/gradle/internal/initialization/BuildLogicFiles.java#L24-L53
[gradle-script-langs]: https://github.com/gradle/gradle/blob/v9.9.1/platforms/core-runtime/build-discovery/src/main/java/org/gradle/internal/scripts/ScriptingLanguages.java#L30-L37
[gradle-build-dir]: https://github.com/gradle/gradle/blob/v9.9.1/subprojects/core-api/src/main/java/org/gradle/api/Project.java#L240
[gradle-layout]: https://github.com/gradle/gradle/blob/v9.9.1/platforms/core-configuration/file-operations/src/main/java/org/gradle/api/internal/file/DefaultProjectLayout.java#L71
[gradle-base-plugin]: https://docs.gradle.org/9.9.1/userguide/base_plugin.html#sec:base_tasks
[gradle-buildsrc]: https://docs.gradle.org/9.9.1/userguide/sharing_build_logic_between_subprojects.html
[gradle-project-cache]: https://docs.gradle.org/9.9.1/userguide/directory_layout.html#dir:caches:project_local
[gradle-cli]: https://docs.gradle.org/9.9.1/userguide/command_line_interface.html
[gradle-cleanup]: https://docs.gradle.org/9.9.1/userguide/directory_layout.html#dir:gradle_user_home:cache_cleanup
[gradle-retention]: https://github.com/gradle/gradle/blob/v9.9.1/subprojects/core/src/main/java/org/gradle/api/internal/cache/DefaultCacheConfigurations.java#L72-L79
[gradle-caches-doc]: https://docs.gradle.org/9.9.1/userguide/directory_layout.html#dir:caches:shared
[gradle-user-id]: https://github.com/gradle/gradle/blob/v9.9.1/subprojects/core/src/main/java/org/gradle/internal/scopeids/DefaultPersistentScopeIdLoader.java#L32-L35
[gradle-user-home-secure]: https://docs.gradle.org/9.9.1/userguide/securing_build_tool.html#sec:user_home
[gradle-user-home]: https://docs.gradle.org/9.9.1/userguide/directory_layout.html#dir:gradle_user_home
[gradle-version-info]: https://github.com/gradle/gradle/blob/v9.9.1/platforms/core-runtime/launcher/src/main/java/org/gradle/launcher/cli/internal/VersionInfoRenderer.java#L47-L71
[gradle-user-home-lookup]: https://github.com/gradle/gradle/blob/v9.9.1/platforms/core-runtime/build-discovery/src/main/java/org/gradle/initialization/BuildLayoutParameters.java#L61-L70
[gradle-user-home-cli]: https://github.com/gradle/gradle/blob/v9.9.1/platforms/core-runtime/launcher/src/main/java/org/gradle/launcher/cli/converter/BuildLayoutConverter.java#L57-L59
[gradle-cache-locking]: https://docs.gradle.org/9.9.1/userguide/dependency_caching.html#sec:cache-locking
[maven-purge]: https://maven.apache.org/plugins/maven-dependency-plugin/purge-local-repository-mojo.html
[maven-user-files]: https://github.com/apache/maven/blob/maven-3.10.0/maven-embedder/src/main/java/org/apache/maven/cli/MavenCli.java#L144-L157
[maven-security]: https://github.com/apache/maven/blob/maven-3.10.0/maven-core/src/main/java/org/apache/maven/internal/secdispatcher/SecDispatcherProvider.java#L52
[maven-default-repo]: https://github.com/apache/maven/blob/maven-3.10.0/maven-core/src/main/java/org/apache/maven/repository/RepositorySystem.java#L50-L56
[maven-settings-merge]: https://github.com/apache/maven/blob/maven-3.10.0/maven-settings-builder/src/main/java/org/apache/maven/settings/merge/MavenSettingsMerger.java#L81-L83
[maven-repo-local]: https://github.com/apache/maven/blob/maven-3.10.0/maven-embedder/src/main/java/org/apache/maven/cli/MavenCli.java#L1527-L1537
[maven4-constants]: https://github.com/apache/maven/blob/maven-4.0.0-rc-7/api/maven-api-core/src/main/java/org/apache/maven/api/Constants.java#L85-L199
[maven4-repo-order]: https://github.com/apache/maven/blob/maven-4.0.0-rc-7/impl/maven-cli/src/main/java/org/apache/maven/cling/invoker/LookupInvoker.java#L768-L794
[maven-help-evaluate]: https://maven.apache.org/plugins/maven-help-plugin/evaluate-mojo.html
[maven-install-file]: https://github.com/apache/maven-install-plugin/blob/maven-install-plugin-3.2.0/src/main/java/org/apache/maven/plugins/install/InstallFileMojo.java#L261-L331
[resolver-installer]: https://github.com/apache/maven-resolver/blob/maven-resolver-2.0.24/maven-resolver-impl/src/main/java/org/eclipse/aether/internal/impl/DefaultInstaller.java#L193-L216
[resolver-local-metadata]: https://github.com/apache/maven-resolver/blob/maven-resolver-2.0.24/maven-resolver-impl/src/main/java/org/eclipse/aether/internal/impl/SimpleLocalRepositoryManager.java#L80-L91
[resolver-metadata-name]: https://github.com/apache/maven-resolver/blob/maven-resolver-2.0.24/maven-resolver-impl/src/main/java/org/eclipse/aether/internal/impl/DefaultLocalPathComposer.java#L71-L129
[maven-versions-metadata]: https://github.com/apache/maven/blob/maven-3.10.0/maven-resolver-provider/src/main/java/org/apache/maven/repository/internal/VersionsMetadataGenerator.java#L89-L98
[resolver-remote-metadata]: https://github.com/apache/maven-resolver/blob/maven-resolver-2.0.24/maven-resolver-impl/src/main/java/org/eclipse/aether/internal/impl/DefaultMetadataResolver.java#L262
[resolver-locks]: https://github.com/apache/maven-resolver/blob/maven-resolver-2.0.24/maven-resolver-impl/src/main/java/org/eclipse/aether/internal/impl/synccontext/named/BasedirNameMapper.java#L43-L52
