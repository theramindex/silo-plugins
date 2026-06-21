import { readFileSync } from "node:fs";
import path from "node:path";

const manifestPath = new URL("../manifest.json", import.meta.url);
const checksumsPath = new URL("../checksums.txt", import.meta.url);

const catalogDisplayNotes = new Map([
  [
    "silo.ramindex.local-metadata",
    {
      displayName: "Local Metadata: NFO Sidecars",
      description: "Read-only Jellyfin-compatible NFO and local artwork metadata provider for Silo.",
    },
  ],
  [
    "silo.ramindex.dispatcharr",
    {
      displayName: "Live TV",
      description: "Dispatcharr-backed IPTV app with Live TV, guide, favorites, VOD catalog, and series catalog routes.",
    },
  ],
  [
    "silo.ramindex.app-links",
    {
      displayName: "App Links",
      description: "Configurable external app launcher with fullscreen iframe shells and an admin app link manager.",
    },
  ],
]);

const index = JSON.parse(readFileSync(manifestPath, "utf8"));
const checksumLines = readFileSync(checksumsPath, "utf8")
  .split(/\r?\n/)
  .map((line) => line.trim())
  .filter(Boolean);

const checksums = new Map();
for (const line of checksumLines) {
  const match = line.match(/^([a-f0-9]{64})\s+\*?(.+)$/i);
  if (!match) {
    throw new Error(`Invalid checksum line: ${line}`);
  }
  checksums.set(path.basename(match[2]), match[1].toLowerCase());
}

if (!Array.isArray(index.plugins) || index.plugins.length === 0) {
  throw new Error("manifest.json must contain a non-empty plugins array");
}

const seen = new Set();
for (const pkg of index.plugins) {
  const manifest = pkg.manifest;
  if (!manifest?.plugin_id) {
    throw new Error("catalog package is missing manifest.plugin_id");
  }
  if (!manifest.version) {
    throw new Error(`${manifest.plugin_id} is missing manifest.version`);
  }
  if (!manifest.silo_api_version) {
    throw new Error(`${manifest.plugin_id} is missing manifest.silo_api_version`);
  }
  if (!Array.isArray(manifest.capabilities) || manifest.capabilities.length === 0) {
    throw new Error(`${manifest.plugin_id} must declare capabilities`);
  }

  const expectedDisplayNote = catalogDisplayNotes.get(manifest.plugin_id);
  if (!expectedDisplayNote) {
    throw new Error(`${manifest.plugin_id} is missing from catalogDisplayNotes`);
  }
  const primaryCapability = manifest.capabilities[0];
  if (primaryCapability.display_name !== expectedDisplayNote.displayName) {
    throw new Error(
      `${manifest.plugin_id} first capability display_name must be "${expectedDisplayNote.displayName}"`,
    );
  }
  if (primaryCapability.description !== expectedDisplayNote.description) {
    throw new Error(`${manifest.plugin_id} first capability description must preserve the catalog note`);
  }

  const key = `${manifest.plugin_id}@${manifest.version}`;
  if (seen.has(key)) {
    throw new Error(`duplicate catalog entry ${key}`);
  }
  seen.add(key);

  if (!pkg.binaries || Object.keys(pkg.binaries).length === 0) {
    throw new Error(`${key} is missing binaries`);
  }
  if (!pkg.binaries["linux/amd64"]?.url) {
    throw new Error(`${key} is missing linux/amd64 binary url`);
  }
  for (const [platform, binary] of Object.entries(pkg.binaries)) {
    if (!binary?.url) {
      throw new Error(`${key} ${platform} binary is missing url`);
    }
    const filename = path.basename(new URL(binary.url).pathname);
    const expectedChecksum = checksums.get(filename);
    if (!expectedChecksum) {
      throw new Error(`${key} binary ${filename} is missing from checksums.txt`);
    }
    if (binary.checksum?.toLowerCase() !== expectedChecksum) {
      throw new Error(`${key} ${platform} binary checksum does not match checksums.txt`);
    }
  }
  const linuxAMD64Filename = path.basename(new URL(pkg.binaries["linux/amd64"].url).pathname);
  const linuxAMD64Checksum = checksums.get(linuxAMD64Filename);
  if (manifest.checksum?.toLowerCase() !== linuxAMD64Checksum) {
    throw new Error(`${key} manifest checksum must match the linux/amd64 binary checksum`);
  }
}

console.log(`Validated ${index.plugins.length} catalog plugins`);
