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

## How to read the tables

The [README](../README.md#try-a-safe-run) defines roots, projects, caches,
and the quiet, active, and busy states.
For every row:

- `scan` reports matching project caches and deletes nothing.
  `scan --global` also lists global rules, including clear rules.
- `clean` shows a dry run for quiet projects.
  Add `--apply` to delete selected project caches after the path and Git checks.
  Busy projects are excluded; `--include-active` adds active projects.
- `clean --global` shows global cleanup actions instead of project caches.
  Add `--apply` to run eligible prune actions.
  It does not use project activity or Git checks.
- `--include-clear` requires `clean --global` and adds clear actions.
  It still deletes nothing without `--apply`.

Project cache names are exact directory names.
Every match must be inside a scanned root, ignored by Git,
and contain no tracked files.
A listed project file must be in the cache directory's parent directory.
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
`~/.local/share/mise/installs` and adds a note.
That fixed path is also used to estimate freed space.
Override `mise-tools` in `rules.toml` if your installs directory differs;
copy its `check`, `check_env`, `refuse`, `refuse_reason`, and `targets` too.
The command itself follows mise's environment and settings.
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
| `dotnet` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no .NET cache rule. `clean --global` selects mise rules; `--include-clear` adds no .NET rule. | Project `bin` and `obj`; NuGet global packages and HTTP cache. |
| `dotnet-core` (alias of `dotnet`) | Guarded `mise-tools`; same tool as `dotnet` | none yet | none yet | Same as `dotnet`: no project rule; global mise rules only; no additional clear rule. | Same .NET and NuGet gaps as `dotnet`. |
| `elixir` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Elixir cache rule. `clean --global` selects mise rules; `--include-clear` adds no Elixir rule. | Mix `_build` and `deps`; Hex package cache. |
| `erlang` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Erlang cache rule. `clean --global` selects mise rules; `--include-clear` adds no Erlang rule. | Rebar3 `_build` and global package cache. |
| `go` | Guarded `mise-tools` | none yet | `go-modules`: `go clean -modcache` (clear); `go-build`: `go clean -cache` (clear) | `scan` and `clean` have no Go project rule. `clean --global` selects mise rules; `--include-clear` adds both Go rules. | Project binaries, `vendor`, and caches at paths other than those returned by Go from the home directory. |
| `java` | Guarded `mise-tools` | `.gradle`; `target` beside `pom.xml` | none yet | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds no Java rule. | Maven's local repository (`~/.m2`); Gradle's global cache (`~/.gradle`); Gradle project `build` output. |
| `node` | Guarded `mise-tools` | `node_modules`; `dist` beside `package.json`; `.astro`, `.next`, `.parcel-cache`, `.svelte-kit`, `.turbo` | `pnpm`: `pnpm store prune` (prune); `npm`: `npm cache clean --force` (clear); `yarn`: `yarn cache clean` (clear, Yarn 1) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and `pnpm`; `--include-clear` adds `npm` and `yarn`. | Yarn 2+ caches such as `.yarn/cache`; separate pnpm stores not resolved from the home directory; generic `build` output. |
| `python` | Guarded `mise-tools` | `.venv`, `__pycache__`, `.mypy_cache`, `.pytest_cache`, `.ruff_cache`; `venv` beside `pyproject.toml`, `requirements.txt`, or `setup.py` | `uv`: `uv cache prune` (prune); `pip`: `pip cache purge` (clear) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and `uv`; `--include-clear` adds `pip`. | `.tox`, `.nox`, Python `build` output, and `dist` without `package.json`; uv-managed Python installations outside mise; caches at other configured paths. |
| `ruby` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Ruby cache rule. `clean --global` selects mise rules; `--include-clear` adds no Ruby rule. | Bundler's `vendor/bundle` and `vendor/cache`; RubyGems' gem cache; application `tmp/cache`. |
| `rust` | Guarded `mise-tools` | `target` beside `Cargo.toml` | none yet | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules; `--include-clear` adds no Rust rule. | Cargo's registry and Git caches (`~/.cargo/registry`, `~/.cargo/git`); sccache; toolchains managed outside mise. |
| `swift` | Guarded `mise-tools` | `.build`, `DerivedData` | `xcode-derived-data`: built-in deletion of stale Xcode build directories (prune, macOS) | `scan` reports project matches; `clean` selects quiet ones. `clean --global` selects mise rules and the Xcode rule when available; `--include-clear` adds no Swift rule. | Swift Package Manager's global cache; `.swiftpm`; Xcode build data for existing workspaces, archives, device support, and simulators. |
| `zig` | Guarded `mise-tools` | none yet | none yet | `scan` and `clean` have no Zig cache rule. `clean --global` selects mise rules; `--include-clear` adds no Zig rule. | `.zig-cache`, older `zig-cache`, `zig-out`, and Zig's global cache. |

"None yet" means there is no dedicated rule for that cache.
An unrelated directory name can still match a generic project rule.
Outside the shared mise rules, the core tools with no cache rule are
`dotnet` (also named `dotnet-core`), `elixir`, `erlang`, `ruby`, and `zig`.
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
| `dotnet` | .NET tool installs and NuGet package/HTTP caches | `mise-tools` removes selected installs | NuGet caches have no rule ([source][mise-dotnet]) |
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
| Maven, Gradle | `target` beside `pom.xml`; `.gradle` | none yet | Delete matching project directories | `scan` reports; `clean` selects quiet projects; `--apply` deletes. Global repositories and caches remain. |
| uv | `.venv`; `venv` beside `pyproject.toml`, `requirements.txt`, or `setup.py` | `uv` | `uv cache prune` | prune; eligible with `clean --global`; requires `--apply` to run |
| pip | The same Python environment names | `pip` | `pip cache purge` | clear; requires `clean --global --include-clear --apply` to run |
| npm | `node_modules` | `npm` | `npm cache clean --force` | clear; requires `clean --global --include-clear --apply` to run |
| pnpm | `node_modules` | `pnpm` | `pnpm store prune` | prune; eligible with `clean --global`; requires `--apply` to run |
| Yarn 1 | `node_modules` | `yarn` | `yarn cache clean` | clear; requires `clean --global --include-clear --apply` to run. Yarn 2+ has no matching global rule. |
| Homebrew | none yet | `homebrew` | `brew cleanup --prune=all` | clear; requires `clean --global --include-clear --apply` to run. Homebrew can remove old installed versions as well as cached downloads; Plonge measures only `brew --cache`. |
| Xcode | `DerivedData` | `xcode-derived-data` | Built-in deletion of stale directories under `~/Library/Developer/Xcode/DerivedData`; no external command | prune; macOS only, when that directory can be read. Eligible with `clean --global`; requires `--apply` to delete. |

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
