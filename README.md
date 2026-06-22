# polkadot

An application to generate dotfiles from https://github.com/taskie/dotfiles .

## Usage

```
polkadot [-n] [-V] <component-dir> [<component-dir> ...]
```

- `-n` — dry run; resolve everything but don't write any files.
- `-V` — print the version and exit.

`polkadot` runs from your dotfiles root (the current working directory), which
must contain `entry.yml`. The positional arguments are *component directories*
that hold the fragments and config to assemble.

### Example

Lay out a dotfiles repo like this:

```
dotfiles/
├── entry.yml            # tags that describe this machine
└── common/              # a component directory
    ├── tags.yml         # tag dependency graph
    ├── rules.yml        # which fragments build which output files
    ├── paths.yml        # resolve tag values from the host
    └── bash/
        ├── 00-base.sh         # always included
        ├── 10-linux_linux.sh  # included only when the `linux` tag is set
        └── 20-prompt_gtp.sh   # rendered as a Go template (`gtp` tag)
```

`entry.yml` activates the tags for the current host (an empty value defaults to
the key name):

```yaml
linux:
arch:
```

`common/rules.yml` maps an output file to the fragments that compose it:

```yaml
~/.bashrc:
  dir: /bash
  pat: \.sh$
  mode: "644"
```

`common/paths.yml` resolves tag values by probing the system:

```yaml
emacs:
  - type: exec   # value becomes the absolute path found via `which`
```

Generate the dotfiles:

```sh
cd path/to/dotfiles
polkadot -n common   # preview what would be written
polkadot common      # write the files (here, ~/.bashrc)
```

For each output file, matching fragments are concatenated in sorted order; a
fragment is included only when every tag encoded in its filename
(`name_tag1_tag2.ext`) is active. Fragments tagged `gtp` are rendered with Go's
`text/template`, receiving the resolved tag map as their data.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full pipeline.

## License

Apache 2.0
