# Architecture

`polkadot` is a small, single-binary command-line tool that **generates dotfiles**
by assembling fragments scattered across one or more component directories. It is
the engine behind [taskie/dotfiles](https://github.com/taskie/dotfiles): given a
set of *tags* describing the current machine (OS, distro, installed programs,
preferences), it selects the relevant fragments, optionally renders them as
templates, concatenates them, and writes the resulting dotfiles into your home
directory.

The whole program lives in one file, `polkadot.go` (~800 lines). There are no
internal packages; the design is a linear pipeline expressed as methods on a
single `App` struct.

## At a glance

- **Language / module:** Go (`module github.com/taskie/polkadot`, Go 1.21).
- **Dependencies:** `gopkg.in/yaml.v2` (config parsing), `github.com/fatih/color`
  (colored progress output). Everything else is the standard library.
- **Entry point:** `main()` → `run()` in `polkadot.go`.
- **Release:** GoReleaser (`.goreleaser.yml`) builds static (`CGO_ENABLED=0`)
  binaries for linux/windows/darwin.
- **Tests:** `polkadot_test.go` covers the tag `Expander` (the trickiest part).

## Invocation

```
polkadot [-c <polkadot.yml>] [-n] [-raw] [-V] [<component-dir> ...]
```

- `-c` — path to a `polkadot.yml` config. Defaults to `./polkadot.yml` if it
  exists.
- `-n` — dry run: do everything except write output files.
- `-raw` — concatenate fragments verbatim (no newline normalization).
- `-V` — print version and exit.
- positional args — the *component directories* (`polkaDirPaths`) to scan.
  When given, they **replace** `components` from `polkadot.yml`.

`NewApp` resolves these inputs (CLI > `polkadot.yml` > defaults):

- **With `polkadot.yml`:** the directory containing it is the dotfiles root
  (`dotfilesDirPath`), and every relative path in it (`entries`, `components`)
  is resolved against that directory, independent of the cwd.
- **Without it (legacy):** the current working directory is the dotfiles root
  and must contain `entry.yml`; components come from the positional args.

Component directories are scanned in the order given; later directories
override earlier ones for same-keyed config.

### `polkadot.yml`

```yaml
entries:        # entry files merged in order (later wins); default [entry.yml],
  - entry.yml   # `entries: []` disables it
  - hosts/myhost.yml
tags:           # inline entry tags, merged after the entry files
  wsl:
components:     # component dirs, in override order
  - common
  - linux
raw: false      # same as -raw
```

Unknown keys are rejected (`yaml.UnmarshalStrict`) to catch typos.

## Core data model

Everything funnels into one **tag map** (`map[string]string`): tag name → value
(usually a resolved path or an identifier). A fragment is included only if *all*
the tags encoded in its filename are present in this map. Templated fragments
additionally receive the tag map as their template context.

The central in-memory state is the `App` struct, whose fields mirror the pipeline
stages:

| Field | Stage | Meaning |
|-------|-------|---------|
| `dotfilesDirPath`, `entryPath`, `polkaDirPaths` | Input | CLI-derived inputs |
| `entryTags` | Load | tags declared in the entry files and inline `tags` |
| `tagConf` | Load | tag → implied-child-tags graph (`tags.yml`) |
| `ruleConfMap` | Load | output file → weave rule (`rules.yml`) |
| `tagMap` | Collect | the final resolved tag map |
| `dotEntries` | Weave | output files paired with their source fragments |

## Pipeline

`run()` builds an `App` and calls `Prepare()` then (unless `-n`) `Execute()`.
`Prepare()` orchestrates five stages; `Execute()` runs the sixth.

```
entry.yml ─┐
tags.yml ──┤  LoadEntry / LoadTags / LoadRules
rules.yml ─┘            │
                        ▼
            Expand  (resolve the tag graph, honor negation)
                        │
                        ▼
paths.yml ──►  Collect (probe the system: exec/file/dir/env)
                        │
                        ▼  → tagMap
              Weave  (scan component dirs, match files to rules,
                      filter by filename tags) → dotEntries
                        │
                        ▼
             Generate (concat fragments, render templates, write files)
```

### 1. Load (`LoadEntry`, `LoadTags`, `LoadRules`)

- **entry files** (`entry.yml` in the dotfiles root, or the `entries` list of
  `polkadot.yml`) — each a flat `map[string]string` of the tags this machine
  should activate. Files are merged in order, then inline `tags` from
  `polkadot.yml` on top. An empty value defaults to the key itself. A
  built-in `default: default` tag is always added.
- **`<dir>/tags.yml`** — a `map[tag]map[childTag]value`: declaring a tag pulls in
  its child tags. This forms the dependency graph expanded in stage 2. Loaded
  from every component dir; later dirs overwrite earlier definitions per tag.
- **`<dir>/rules.yml`** — `map[outputFile]WeaverEntry`. Each rule says which
  source subdirectories to scan (`dir` / `dirs`), a regexp `pat` selecting files,
  and an optional octal `mode` for the generated file. Parsed into `WeaverRule`
  (with a compiled `*regexp.Regexp` and a `*int` mode validated to `0..0777`).

### 2. Expand (`Expander`)

Turns the declared `entryTags` into a closed set of accepted/rejected tags by a
**breadth-first walk** over the `tags.yml` graph. Key rules:

- A tag prefixed with `!` is **negated** (rejected). Multiple `!`s encode both
  *parity* (odd = negative) and *importance* (the count) — so `!!tag` is a
  double-negative that re-accepts, and higher `!` counts win conflicts.
- Results are sorted by importance desc, then BFS depth, then name/value, then
  de-duplicated so the nearest/strongest declaration of each tag wins.
- Output: `acceptedTags` and `rejectedTags` maps.

This is the most subtle logic in the codebase and is the focus of
`polkadot_test.go` (regular, negation, and double-negative cases).

### 3. Collect (`Collector`, `paths.yml`)

Probes the host system to resolve dynamic tag values. **`<dir>/paths.yml`** maps
a tag key to a list of candidate `CollectorEntry` items; the first that resolves
wins. Entry `type`s:

- `exec` — `exec.LookPath(name)`; value = absolute path to the executable.
- `file` / `dir` — `os.Stat` a path (with `~/` expansion) and check it is the
  right kind; value = absolute path.
- `env` — value = `os.Getenv(name)`.

The collected map is then merged into `tagMap` along with the built-in
`dotfiles` (the root path) and `gtp` tags, the `acceptedTags` are layered on top,
and `rejectedTags` are deleted. The result is the authoritative `tagMap`.

### 4. Weave (`Weaver`)

For each rule, walks `<rootDir><ruleDir>` across every component dir and every
configured subdirectory, keeping files whose name matches the rule's regexp.

- **Filename tagging:** a fragment's basename (extension stripped recursively) is
  split on `_`; everything after the first segment is treated as required tags
  (`extractTagsFromPath`). A fragment is kept only if **all** its tags are in
  `tagMap`. This is how machine-specific fragments are switched on/off.
- Matching sources are grouped per output file, sorted by name, and
  de-duplicated by path (`mergeSourceArrayMap` / `removeDuplicatedDotSource`).
- Output is a sorted `[]DotEntry`, each pairing a `DotTarget` (output path +
  mode) with its ordered `[]DotSource`. Sorting makes the build deterministic.

### 5. Generate (`Generator`)

For each `DotEntry`: expand `~/` in the target path, `mkdir -p` the parent
directory, open the file with the rule's mode (default `0644`), and **concatenate
all source fragments** into it.

- Fragments tagged `gtp` (the built-in "go-template" tag) are rendered through
  Go's `text/template` with `tagMap` as the data context.
- All other fragments are copied verbatim.

## Conventions a component directory must follow

A component (polka) directory may contain any of these config files (all
optional, all merged across multiple directories):

- `tags.yml` — tag dependency graph.
- `rules.yml` — output file ⇒ which source dirs/patterns/mode.
- `paths.yml` — how to resolve tag values from the host.
- source fragment files under the directories named by `rules.yml`, named
  `something_tag1_tag2.ext` to gate them on tags.

The dotfiles root (the directory of `polkadot.yml`, or the cwd in legacy mode)
supplies `entry.yml` (or the files listed in `entries`), the per-machine tag
declaration.

## Notable design choices

- **Single file, no abstractions over the filesystem.** Each stage is a small
  struct (`Collector`, `Expander`, `Weaver`, `Generator`) with one public method;
  `App` wires them together. Easy to read top-to-bottom.
- **Determinism by sorting** at the merge and entry-assembly steps, so repeated
  runs produce identical output.
- **Layered overrides** everywhere: multiple component dirs are processed in
  order and later ones win, enabling a base + per-host layering scheme.
- **Fail fast, log loudly.** Errors bubble up to `main()`, which prints a red
  "Failed" and exits non-zero; progress is narrated with colored headers and the
  resolved tag maps / source lists are logged for debugging.
