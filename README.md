# The RamIndex Silo Plugin Catalog

This repository is a remote install catalog for Silo plugins. Add the raw
manifest URL below to Silo to browse and install the published plugins:

```text
https://raw.githubusercontent.com/theramindex/silo-plugins/main/manifest.json
```

The catalog manifest contains the current version, capabilities, supported
platforms, release asset URLs, and SHA-256 checksums for each plugin. Release
assets may be published in the catalog repository or in the plugin's source
repository, depending on the plugin's release workflow.

## Included plugins

The versions and capabilities below are taken from the current `manifest.json`.

| Plugin | Version | Category | Description | Platforms |
| --- | ---: | --- | --- | --- |
| [Dispatcharr for Silo](https://github.com/theramindex/silo-plugin-dispatcharr)  (`silo.ramindex.dispatcharr`) | `0.3.101` | Video / Live TV | Dispatcharr-backed Live TV app with guide, favorites, VOD, and series catalog routes. DVR/Recordings are available only in Dispatcharr Direct mode, not Xtream Codes or M3U/XMLTV mode. | macOS ARM64, Linux AMD64, Linux ARM64 |
| [App Links](https://github.com/theramindex/silo-plugin-app-links)  (`silo.ramindex.app-links`) | `0.1.1` | Apps | Configurable external app launcher with fullscreen iframe shells and an admin app-link manager. | macOS ARM64, Linux AMD64, Linux ARM64 |
| [XC for Silo](https://github.com/theramindex/silo-plugin-xtream-library)  (`silo.ramindex.xtream`) | `0.2.69` | Live TV | Xtream Codes-first Live TV app with guide, VOD, series, provider catch-up, and multiview. It also supports M3U channel catalogs and guide sources. | macOS ARM64, Linux AMD64, Linux ARM64 |
| [OIDC Sign-In](https://github.com/theramindex/silo-plugin-oidc)  (`silo.ramindex.oidc`) | `0.1.0` | Authentication | OpenID Connect sign-in provider for Silo using OAuth 2.0. | macOS ARM64, Linux AMD64, Linux ARM64 |

## Installing in Silo

1. Open Silo admin settings.
2. Go to **Plugins**.
3. Add a plugin repository using the manifest URL above.
4. Refresh the plugin catalog.
5. Install the plugin you need.

## Validating catalog changes

Run the catalog validator before publishing changes:

```sh
node scripts/validate-catalog.mjs
```

The published binaries currently cover macOS ARM64, Linux AMD64, and Linux
ARM64 where supported by the individual plugin entry.
