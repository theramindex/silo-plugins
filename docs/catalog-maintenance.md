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
    "plugin_id": "silo.ramindex.app-links",
    "version": "0.1.1",
    "checksum": "<linux-amd64-binary-sha256>",
    "silo_api_version": "v1",
    "capabilities": [
      {
        "type": "http_routes.v1",
        "id": "app-links-routes",
        "display_name": "App Links",
        "description": "Configurable external app launcher with fullscreen iframe shells and an admin app link manager."
      }
    ]
  },
  "repo_url": "https://github.com/theramindex/silo-plugin-app-links",
  "checksums_url": "https://github.com/theramindex/silo-plugin-app-links/releases/download/v0.1.1/checksums.txt",
  "binaries": {
    "linux/amd64": {
      "url": "https://github.com/theramindex/silo-plugin-app-links/releases/download/v0.1.1/app-links-0.1.1-linux-amd64",
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

5. Publish the release assets from the plugin repository. If a workflow updates
   this catalog, give that plugin repository a `CATALOG_PUSH_TOKEN` secret with
   write access to this catalog repository.
6. Read back the published raw manifest and validate it.
