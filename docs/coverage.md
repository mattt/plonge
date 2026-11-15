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
No other default rule uses the name `build`.
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
so the rules use the default path for each system,
and a cache moved by `XDG_CACHE_HOME` is measured at the wrong path.
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

## How to read the tables

The [README](../README.md#try-a-safe-run) defines roots, projects, caches,
and the quiet, active, and busy states.
For every row:

- `scan` reports matching project caches and deletes nothing.
  `scan --global` also lists global rules, including clear rules.
- `clean` shows a dry run for quiet projects.
  Add `--apply` to delete selected project caches after the path and Git checks.
  Busy projects are excluded; `--include-active` adds active projects.
  If `paseo` is on PATH but fails, every project is busy.
- `clean --global` shows global cleanup actions instead of project caches.
  Add `--apply` to run eligible prune actions.
  It does not use project activity or Git checks.
- `--include-clear` requires `clean --global` and adds clear actions.
  It still deletes nothing without `--apply`.

Project cache names are exact directory names.
Every match must be inside a scanned root, ignored by Git,
and contain no tracked files.
A listed project file must be in the cache directory's parent directory.
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
| `bun` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` if used | Bun-specific: none yet | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds no Bun-specific rule. | Bun's package cache (`~/.bun/install/cache`) and runtime caches outside matching project directories. |
| `deno` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` if used | Deno-specific: none yet | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds no Deno-specific rule. | Deno's dependency and compiled-code cache (`DENO_DIR`). |
| `dotnet` | Guarded `mise-tools` | `bin` and `obj` beside `*.csproj`, `*.fsproj`, or `*.vbproj` | `nuget-packages`: `dotnet nuget locals global-packages --clear` (clear); `nuget-http-cache`: `dotnet nuget locals http-cache --clear` (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds both NuGet rules. | Output moved by `BaseOutputPath`, `BaseIntermediateOutputPath`, or `--output`; the [`artifacts` layout][dotnet-artifacts] (`UseArtifactsOutput`); `bin` and `obj` beside other project types; NuGet's `temp` and `plugins-cache` folders; `packages.config` folders set by `repositoryPath`; NuGet folders set only in a project's `nuget.config`. |
| `dotnet-core` (alias of `dotnet`) | Guarded `mise-tools`; same tool as `dotnet` | Same as `dotnet` | Same as `dotnet` | Same as `dotnet`. | Same as `dotnet`. |
| `elixir` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Elixir cache rule. `clean --global` selects mise rules; `--include-clear` adds no Elixir rule. | Mix `_build` and `deps`; Hex package cache. |
| `erlang` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Erlang cache rule. `clean --global` selects mise rules; `--include-clear` adds no Erlang rule. | Rebar3 `_build` and global package cache. |
| `go` | Guarded `mise-tools` | none yet | `go-modules`: `go clean -modcache` (clear); `go-build`: `go clean -cache` (clear) | `scan` and `clean` have no Go project rule. `clean --global` selects mise rules; `--include-clear` adds both Go rules. | Project binaries, `vendor`, and caches at paths other than those returned by Go from the home directory. |
| `java` | Guarded `mise-tools` | `.gradle`; `target` beside `pom.xml` | none yet | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds no Java rule. | Maven's local repository (`~/.m2`); Gradle's global cache (`~/.gradle`); Gradle project `build` output, unless `pyproject.toml` or `setup.py` is beside it. |
| `node` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` | `pnpm`: `pnpm store prune` (prune); `npm`: `npm cache clean --force` (clear); `yarn`: `yarn cache clean` (clear, Yarn 1) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and `pnpm`; `--include-clear` adds `npm` and `yarn`. | Yarn 2+ caches such as `.yarn/cache`; separate pnpm stores not resolved from the home directory; `build` output, unless `pyproject.toml` or `setup.py` is beside it. |
| `python` | Guarded `mise-tools` | `.venv`, `__pycache__`, `.mypy_cache`, `.pytest_cache`, `.ruff_cache`, `.tox`, `.nox`; `venv` beside `pyproject.toml`, `requirements.txt`, or `setup.py`; `build` and `dist` beside `pyproject.toml` or `setup.py` | `uv`: `uv cache prune` (prune); `pip`: `pip cache purge` (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and `uv`; `--include-clear` adds `pip`. | tox and nox environments moved by `--workdir`, `work_dir`, or `--envdir`; build output moved by setuptools' `build_base`; `*.egg-info`; `dist` moved by `--outdir` or `--out-dir`; uv-managed Python installations outside mise; caches at other configured paths. |
| `ruby` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Ruby cache rule. `clean --global` selects mise rules; `--include-clear` adds no Ruby rule. | Bundler's `vendor/bundle` and `vendor/cache`; RubyGems' gem cache; application `tmp/cache`. |
| `rust` | Guarded `mise-tools` | `target` beside `Cargo.toml` | none yet | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds no Rust rule. | Cargo's registry and Git caches (`~/.cargo/registry`, `~/.cargo/git`); sccache; toolchains managed outside mise. |
| `swift` | Guarded `mise-tools` | `.build`, `DerivedData` | `swiftpm-macos` (macOS) or `swiftpm-linux` (Linux): `swift package purge-cache` (clear); `xcode-derived-data`: built-in deletion of stale Xcode build directories (prune, macOS) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and the Xcode rule when available; `--include-clear` adds the SwiftPM rule for the system. | SwiftPM's `artifacts` and `prebuilts` caches; a shared cache moved by `--cache-path` or `XDG_CACHE_HOME`; `.swiftpm`, which holds package configuration and Xcode schemes; Xcode build data for existing workspaces, archives, device support, and simulators. |
| `zig` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Zig cache rule. `clean --global` selects mise rules; `--include-clear` adds no Zig rule. | `.zig-cache`, older `zig-cache`, `zig-out`, and Zig's global cache. |

"None yet" means there is no dedicated rule for that cache.
An unrelated directory name can still match a generic project rule.
Outside the shared mise rules, the core tools with no cache rule are
`elixir`, `erlang`, `ruby`, and `zig`.
Bun and Deno have project-name matches but no dedicated global-cache rule.

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
| `cargo` | Cargo registry and Git downloads after source builds; optional compiler caches; prebuilt binaries when available | `mise-tools` removes selected installs; project `target` applies only in scanned roots beside `Cargo.toml` | Cargo registry, Git cache, and sccache have no global rule ([backend][cargo]) |
| `go` | Go module downloads and build cache | `go-modules` and `go-build` clear the paths returned by `go env GOMODCACHE` and `go env GOCACHE` from the home directory | Caches at other configured paths |
| `npm` | Embedded installer cache and store under mise's cache directory, or external npm/pnpm/Bun caches when configured | `mise-cache` reaches the embedded cache; `npm` clears npm's cache; `pnpm` prunes its resolved store | Bun's package cache and other stores not resolved by the rules ([source][mise-npm]) |
| `pypi` | Per-tool environments and uv cache, or pip cache when installing through pipx | `mise-tools` removes selected environments; `uv` prunes and `pip` clears their resolved caches | Other pipx state and environments outside mise; caches at other configured paths ([backend][pypi]) |
| `gem` | Per-tool gems and gem archives under each install; separate RubyGems and Bundler caches | `mise-tools` removes selected installs as a whole | No rule to prune archives within retained installs or separate gem/Bundler caches ([source][mise-gem]) |
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
| tox, nox | `.tox`, `.nox` | none | Delete matching project directories; neither tool has a cleanup command | `scan` reports; `clean` selects quiet projects; `--apply` deletes. Environments moved by `--workdir`, `work_dir`, or `--envdir` are not matched. |
| Maven, Gradle | `target` beside `pom.xml`; `.gradle` | none yet | Delete matching project directories | `scan` reports; `clean` selects quiet projects; `--apply` deletes. Global repositories and caches remain. |
| setuptools | `build` beside `pyproject.toml` or `setup.py` | none | Delete matching project directories; Plonge does not run `setup.py clean --all` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. A `build_base` setting that moves the directory is not followed. |
| build, `uv build` | `dist` beside `pyproject.toml` or `setup.py` | none | Delete matching project directories; neither tool has a command that removes `dist` | `scan` reports; `clean` selects quiet projects; `--apply` deletes. Output moved by `--outdir` or `--out-dir` is not matched. |
| uv | `.venv`; `venv` beside `pyproject.toml`, `requirements.txt`, or `setup.py` | `uv` | `uv cache prune` | prune; eligible with `clean --global`; requires `--apply` to run |
| pip | The same Python environment names | `pip` | `pip cache purge` | clear; requires `clean --global --include-clear --apply` to run |
| npm | `node_modules` | `npm` | `npm cache clean --force` | clear; requires `clean --global --include-clear --apply` to run |
| pnpm | `node_modules` | `pnpm` | `pnpm store prune` | prune; eligible with `clean --global`; requires `--apply` to run |
| Yarn 1 | `node_modules` | `yarn` | `yarn cache clean` | clear; requires `clean --global --include-clear --apply` to run. Yarn 2+ has no matching global rule. |
| Homebrew | none yet | `homebrew` | `brew cleanup --prune=all` | clear; requires `clean --global --include-clear --apply` to run. Homebrew can remove old installed versions as well as cached downloads; Plonge measures only `brew --cache`. |
| Swift Package Manager | `.build` | `swiftpm-macos`, `swiftpm-linux` | `swift package purge-cache`, with a `--scratch-path` in the cache directory | clear; requires `clean --global --include-clear --apply` to run. Each rule applies only on its system. `artifacts` and `prebuilts` remain. |
| Xcode | `DerivedData` | `xcode-derived-data` | Built-in deletion of stale directories under `~/Library/Developer/Xcode/DerivedData`; no external command | prune; macOS only, when that directory can be read. Eligible with `clean --global`; requires `--apply` to delete. |

A global rule can also set `delete_contents` in place of a cleanup command,
for a cache whose tool has none.
Plonge then deletes the contents of the rule's path and keeps the directory.
Such a rule must be a clear rule, so it runs only with
`clean --global --include-clear --apply`.
Plonge skips it when the path is not absolute; is `/`, the home directory,
or contains the home directory; is or resolves through a symlink;
is in a Git repository; or does not exist.
No default rule uses `delete_contents` yet.
See the [README](../README.md#global-caches) for the rule format.

The Xcode global rule selects only directories whose XML `info.plist`
names an absolute `WorkspacePath` that no longer exists.
It keeps directories with missing or unreadable workspace metadata
and workspaces on unmounted volumes under `/Volumes`.
The project `DerivedData` rule is different: it uses the project checks,
without checking Xcode workspace metadata.

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
[cargo]: https://mise.jdx.dev/dev-tools/backends/cargo.html
[pypi]: https://mise.jdx.dev/dev-tools/backends/pypi.html
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
