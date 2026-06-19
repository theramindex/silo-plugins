# Catalog Maintenance

This repository is the installable Silo plugin catalog for The RamIndex plugins.
Silo reads:

```text
https://raw.githubusercontent.com/theramindex/silo-plugins/main/manifest.json
```

## Expected Entry Shape

Each catalog entry includes enough data for list/install discovery:

```json
{
  "manifest": {
    "plugin_id": "silo.local-metadata",
    "version": "0.1.1",
    "checksum": "<linux-amd64-binary-sha256>",
    "silo_api_version": "v1",
    "capabilities": [
      {
        "type": "metadata_provider.v1",
        "id": "local-metadata",
        "display_name": "Local Metadata: NFO Sidecars",
        "description": "Read-only same-basename NFO and local artwork metadata provider for Silo."
      }
    ]
  },
  "repo_url": "https://github.com/theramindex/silo-plugin-local-metadata",
  "checksums_url": "https://github.com/theramindex/silo-plugins/releases/download/v0.1.1/checksums.txt",
  "binaries": {
    "linux/amd64": {
      "url": "https://github.com/theramindex/silo-plugins/releases/download/v0.1.1/plugin-linux-amd64-silo-plugin-local-metadata",
      "checksum": "<linux-amd64-binary-sha256>"
    }
  }
}
```

## Release Checklist

1. Build the plugin binary for each published platform.
2. Generate `checksums.txt` from the exact release binaries.
3. Update `manifest.json` versions, release URLs, and checksums.
4. Run the catalog validator:

```sh
node scripts/validate-catalog.mjs
```

5. Publish the release assets to `theramindex/silo-plugins`; the
   `silo-plugin-local-metadata` release workflow can do this when the plugin
   repository has a `CATALOG_PUSH_TOKEN` secret with write access to this
   catalog repository.
6. Read back the published raw manifest and validate it.
