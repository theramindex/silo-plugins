# Releasing a plugin

Release from GitHub: open the plugin repo's **Actions** tab, choose **Release**, then **Run workflow** on `main`. Everything after that runs as the `ramindex-ci` GitHub App:

1. **Release** (`release-plugin.yml` in this repo) bumps `manifest.json` on `main`, runs the plugin's checks, commits, tags `v<version>` and pushes both.
2. The tag push starts the plugin's tag build, which builds the three binaries and publishes the GitHub release with `checksums.txt`.
3. The tag build's last job (`publish-plugin.yml` in this repo) checks the binaries against `checksums.txt`, updates this catalog with `scripts/update-entry.mjs` (which runs the validator), and pushes the catalog.

Leave **version** blank to release the next patch, or to release the manifest's version when it has no tag yet. Type a version to choose it.

| Plugin repo | Release commit | Tag | Before you run it | Catalog asset prefix | Old checksums |
|---|---|---|---|---|---|
| `silo-plugin-dispatcharr` | `Release <version> <summary>.` (fill in **summary**) | plain | — | `dispatcharr-<version>-` | replaced; entry synced from the plugin manifest |
| `silo-plugin-xtream-library` | `Release XC for Silo <version>` | plain | commit `docs/releases/v<version>.md` to `main` | `xtream-<version>-` | kept |
| `silo-plugin-app-links` | `Release App Links <version>` | plain | — | `app-links-<version>-` | replaced |
| `silo-plugin-local-artwork` | `Release Local Artwork <version>` | annotated, `Local Artwork v<version>` | — | `plugin-` | replaced |
| `silo-plugin-watchprovider-trakt` | `Release Trakt <version>` | plain | — | `trakt-<version>-` | replaced |

Check the run's summary for the catalog commit. The "Validate Catalog" workflow reruns the validator on the push.

## Setup the workflows need

- The organization secret `RAMINDEX_CI_APP_KEY` holds a private key of the `ramindex-ci` GitHub App (app ID 5324952) and is available to every plugin repo. Without it, Release and the catalog job fail at "Get a ramindex-ci token".
- Actions must be enabled in each plugin repo. A new fork runs no workflows until someone enables them on its Actions tab.

## Adding a plugin to the catalog

Add the plugin to `catalogDisplayNotes` in `scripts/validate-catalog.mjs` first; the validator rejects unlisted plugins. Its first catalog publish then creates the entry from the plugin's `manifest.json` (without its presentation text). Give the plugin's tag build a final job like the others:

```yaml
  catalog:
    needs: release
    if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')
    uses: theramindex/silo-plugins/.github/workflows/publish-plugin.yml@main
    with:
      plugin-id: silo.ramindex.<name>
      display-name: <Name>
      asset-prefix: <name>-{version}-
    secrets: inherit
```

Release assets must carry a plugin-specific prefix: `checksums.txt` here is keyed by file name.

## By hand

When a workflow cannot run, release locally instead, as ramindex-ci and through the app's credentials:

1. Bump `manifest.json` on `main`, commit with the message above, tag `v<version>`, and push `main` and the tag together.
2. Download `checksums.txt` and the three binaries from the release and check them with `shasum -a 256 -c`.
3. Update this catalog:

```bash
node scripts/update-entry.mjs --plugin-id <catalog plugin id> --repo <plugin repo name, without theramindex/> \
  --version <version> --asset-prefix <prefix> --checksums <downloaded checksums.txt> \
  [--replace-old] [--plugin-manifest <plugin>/manifest.json [--sync-manifest]]
```

The script changes only the version, checksums and download URLs unless `--sync-manifest` is set, then runs the validator. The catalog's display names and descriptions are its own, pinned in `scripts/validate-catalog.mjs`; they intentionally differ from the plugins' manifests, so leave them alone unless a rename is wanted, and change both files together.

Commit as `Update <plugin name> to <version>` (several plugins: one commit naming each) and push `main`.
