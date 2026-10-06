# Releasing a plugin

A release is three steps: bump the version on the plugin's `main`, push a `v<version>` tag so the plugin's CI builds and publishes the binaries, then point this catalog at the published release. Each step finishes on a check: the release's binaries match its `checksums.txt`, and `node scripts/validate-catalog.mjs` passes.

## 1. Bump and tag the plugin

The version in the plugin's `manifest.json` must equal the tag. Commit the bump on `main`, then tag that commit.

| Plugin repo | Bump commit message | Tag | Also |
|---|---|---|---|
| `silo-plugin-dispatcharr` | `Release <version> <what changed>.` | plain | — |
| `silo-plugin-xtream-library` | `Release XC for Silo <version>` | plain | add `docs/releases/v<version>.md`; run `scripts/verify-release.sh <version>` |
| `silo-plugin-app-links` | `Release App Links <version>` | plain | — |
| `silo-plugin-local-artwork` | `Release Local Artwork <version>` | annotated, message `Local Artwork v<version>` | — |

Push `main` and the tag together. The tag push runs the plugin's CI, which creates the GitHub release with binaries and `checksums.txt`.

## 2. Verify the release

Download `checksums.txt` and the three binaries (darwin-arm64, linux-amd64, linux-arm64) from the release, and check them with `shasum -a 256 -c`. Every binary must match.

## 3. Update the catalog

**Dispatcharr updates the catalog itself:** its CI job "Update Silo catalog" commits to this repo after the release. Check for that commit rather than making your own.

For every other plugin, run:

```bash
node scripts/update-entry.mjs --plugin-id <catalog plugin id> --repo <plugin repo> \
  --version <version> --asset-prefix <prefix> --checksums <downloaded checksums.txt> [--replace-old]
```

| Plugin ID | `--asset-prefix` | `--replace-old` |
|---|---|---|
| `silo.ramindex.xtream` | `xtream-<version>-` | no; `checksums.txt` keeps every xtream release |
| `silo.ramindex.app-links` | `app-links-<version>-` | yes |
| `silo.ramindex.local-metadata` | `plugin-` | yes |

The script changes only the version, checksums and download URLs, then runs the validator. The catalog's display names and descriptions are its own, pinned in `scripts/validate-catalog.mjs`; they intentionally differ from the plugins' manifests, so leave them alone unless a rename is wanted, and change both files together.

Commit as `Update <plugin name> to <version>` (several plugins: one commit naming each) and push `main`. The "Validate Catalog" workflow reruns the validator on GitHub.
