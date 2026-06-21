# The RamIndex Silo Plugin Catalog

This repository is an installable Silo plugin release catalog.

Silo can read this URL as a remote plugin repository:

```text
https://raw.githubusercontent.com/theramindex/silo-plugins/main/manifest.json
```

The catalog points to versioned GitHub release assets in this repository. Each
entry includes:

- the Silo plugin manifest
- a Linux AMD64 binary URL
- an inline SHA-256 checksum
- a `checksums.txt` release asset for operators who want to verify downloads

Validate catalog changes before publishing:

```sh
node scripts/validate-catalog.mjs
```

## Included Plugins

- `silo.ramindex.local-metadata` - read-only Jellyfin-compatible NFO and artwork sidecar
  metadata provider for Silo.
- `silo.ramindex.dispatcharr` - Dispatcharr-backed Live TV app with guide, favorites,
  VOD catalog, and series catalog routes.
- `silo.ramindex.app-links` - configurable external app launcher with fullscreen iframe
  shells and an admin app link manager.

## Installing In Silo

1. Open Silo admin settings.
2. Go to Plugins.
3. Add a plugin repository with the manifest URL above.
4. Refresh the plugin catalog.
5. Install the plugin you need.

The current release assets are Darwin ARM64, Linux AMD64, and Linux ARM64
binaries referenced from each plugin's versioned release.
