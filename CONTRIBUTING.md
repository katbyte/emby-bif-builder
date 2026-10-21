# Contributing

Issues and pull requests are welcome.

- `make check-all` runs everything CI runs. See [development](docs/development.md).
- A video `ebb` gets wrong is easiest to fix with what `ebb lookup` says of it.

## Releases

- Changes collect under "Unreleased" in the [changelog](CHANGELOG.md).
- A version is tagged when a batch is worth upgrading for.
- Breaking changes are grouped into one release, not shipped one at a time.
- Pushing a `v*` tag publishes the Linux and macOS programs and the Homebrew formula.
