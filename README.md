# The Ramedia Server Plugin Catalog

This repository is the Ramedia Server catalog for Silo-compatible plugins. Add the raw
manifest URL below to Silo to browse and install the published plugins:

```text
https://raw.githubusercontent.com/theramindex/silo-plugins/main/manifest.json
```

The catalog manifest contains the current version, capabilities, supported
platforms, release asset URLs, and SHA-256 checksums for each plugin. Release
assets may be published in the catalog repository or in the plugin's source
repository, depending on the plugin's release workflow.

## Included plugins

Current versions, platform coverage and checksums are in [manifest.json](manifest.json).

| Plugin | Description |
| --- | --- |
| [Dispatcharr for Silo](https://github.com/theramindex/silo-plugin-dispatcharr) | Live TV, guide, VOD and series; DVR in Direct mode. |
| [XC for Silo](https://github.com/theramindex/silo-plugin-xtream-library) | Xtream and M3U/XMLTV Live TV, guide, VOD, series and catch-up. |
| [HDHomeRun for Silo](https://github.com/theramindex/silo-plugin-hdhomerun) | HDHomeRun channels, guide, shared playback and Live Rewind. |
| [App Links](https://github.com/theramindex/silo-plugin-app-links) | External app launcher. |
| [Local Artwork](https://github.com/theramindex/silo-plugin-local-artwork) | Local artwork and metadata matching. |
| [Trakt](https://github.com/theramindex/silo-plugin-watchprovider-trakt) | Trakt watch-state integration. |
| [RomM for Silo](https://github.com/theramindex/silo-plugin-romm) | RomM game libraries and browser gameplay. |

## Server compatibility and premiere reminders

Dispatcharr, XC, and HDHomeRun remain usable on Silo servers without the Ramedia Server
premiere extension. Their optional `watchPath` channel metadata is ignored by
older `livetv/v1` consumers; normal channel browsing, guides, and playback do
not depend on the new API. Existing SDK and installation requirements still apply.

With a server advertising `/api/v2/premieres/capabilities`, the web calendar and
series/episode pages can open profile-mapped channels during an expected original
episode premiere. Configure **Calendar → Premiere reminders**. The server owns
scheduling, profile preferences, and PostgreSQL storage. The adapters supply
channel links. EPG matching and DVR are not required; metadata timing cannot
guarantee what any regional feed is showing. Multiple feeds open a chooser.
Native clients do not expose these new controls yet.


## Deprecated plugins

These plugins are no longer offered by the catalog and will not receive
updates. Existing installs keep running until you remove them.

| Plugin | Last version | Replacement |
| --- | ---: | --- |
| [OIDC Sign-In](https://github.com/theramindex/silo-plugin-oidc)  (`silo.ramindex.oidc`) | `0.1.0` | Silo's built-in sign-in |

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

## Web UI development

Use [Modern Web Guidance](docs/modern-web-guidance.md) for plugin browser UI
changes. The [2026-11-13 verification](docs/web-ui-verification-2026-11-13.md)
records known findings, retained patterns and the limits of the local checks.
