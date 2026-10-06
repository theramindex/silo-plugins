#!/usr/bin/env node
// Point one catalog entry at a published plugin release.
//
//   node scripts/update-entry.mjs --plugin-id silo.ramindex.xtream \
//     --repo silo-plugin-xtream-library --version 0.2.72 \
//     --asset-prefix xtream-0.2.72- --checksums /tmp/xc/checksums.txt [--replace-old]
//
// Only the version, checksums and download URLs change. The catalog keeps its own
// presentation text (enforced by validate-catalog.mjs), so nothing is copied from
// the plugin's manifest. --replace-old drops older checksums.txt lines for the
// same asset family; leave it off to keep history (as for xtream-*).
import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";

const manifestPath = new URL("../manifest.json", import.meta.url);
const checksumsPath = new URL("../checksums.txt", import.meta.url);
const platforms = [
  ["darwin/arm64", "darwin-arm64"],
  ["linux/amd64", "linux-amd64"],
  ["linux/arm64", "linux-arm64"],
];

const args = parseArgs(process.argv.slice(2));
const version = required("version").replace(/^v/, "");
const prefix = required("asset-prefix");
const pluginId = required("plugin-id");
const repo = required("repo");

const release = new Map(
  readFileSync(required("checksums"), "utf8")
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => {
      const [digest, name] = line.trim().split(/\s+/);
      return [name.replace(/^\*/, ""), digest.toLowerCase()];
    }),
);

const index = JSON.parse(readFileSync(manifestPath, "utf8"));
const entry = index.plugins.find((plugin) => plugin.manifest?.plugin_id === pluginId);
if (!entry) fail(`catalog does not contain ${pluginId}`);

const base = `https://github.com/theramindex/${repo}/releases/download/v${version}`;
const newLines = new Map();
for (const [key, suffix] of platforms) {
  const name = `${prefix}${suffix}`;
  const checksum = release.get(name);
  if (!checksum) fail(`release checksums have no ${name}`);
  entry.binaries[key] = { url: `${base}/${name}`, checksum };
  newLines.set(name, checksum);
}
entry.manifest.version = version;
entry.manifest.checksum = newLines.get(`${prefix}linux-amd64`);
entry.checksums_url = `${base}/checksums.txt`;
writeFileSync(manifestPath, `${JSON.stringify(index, null, 2)}\n`);

// "xtream-0.2.72-" -> family "xtream-"; "plugin-" stays "plugin-".
const family = prefix.replace(/\d+\.\d+\.\d+-$/, "");
const familyPattern = new RegExp(`^${escape(family)}(\\d+\\.\\d+\\.\\d+-)?(darwin|linux)-(arm64|amd64)$`);
const lines = readFileSync(checksumsPath, "utf8")
  .split(/\r?\n/)
  .filter(Boolean)
  .filter((line) => {
    const name = line.trim().split(/\s+/)[1];
    if (newLines.has(name)) return false;
    return !(args["replace-old"] && familyPattern.test(name));
  });
for (const [name, checksum] of newLines) lines.push(`${checksum}  ${name}`);
lines.sort((a, b) => (sortKey(a) < sortKey(b) ? -1 : sortKey(a) > sortKey(b) ? 1 : 0));
writeFileSync(checksumsPath, `${lines.join("\n")}\n`);

console.log(`${pluginId} -> ${version}`);
execFileSync(process.execPath, [new URL("./validate-catalog.mjs", import.meta.url).pathname], { stdio: "inherit" });

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i].replace(/^--/, "");
    parsed[key] = argv[i + 1]?.startsWith("--") || argv[i + 1] === undefined ? true : argv[++i];
  }
  return parsed;
}

function required(name) {
  if (typeof args[name] !== "string") fail(`missing --${name}`);
  return args[name];
}

function sortKey(line) {
  return line.trim().split(/\s+/)[1];
}

function escape(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function fail(message) {
  console.error(`update-entry: ${message}`);
  process.exit(1);
}
