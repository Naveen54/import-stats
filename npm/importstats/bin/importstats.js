#!/usr/bin/env node
"use strict";

// Thin launcher: finds the prebuilt importstats binary shipped in the matching
// platform package (installed as an optionalDependency) and runs it.

const { spawnSync } = require("child_process");
const path = require("path");

function binaryPath() {
  if (process.env.IMPORTSTATS_BINARY) return process.env.IMPORTSTATS_BINARY;

  const pkg = "importstats-" + process.platform + "-" + process.arch;
  const exe = process.platform === "win32" ? "importstats.exe" : "importstats";
  try {
    return path.join(path.dirname(require.resolve(pkg + "/package.json")), "bin", exe);
  } catch (e) {
    console.error(
      "importstats: no prebuilt binary for " + process.platform + "-" + process.arch + " (" + pkg + " is not installed).\n" +
        "If your platform is supported, reinstall without --no-optional / --omit=optional.\n" +
        "Otherwise download a binary or build from source: https://github.com/Naveen54/import-stats"
    );
    process.exit(1);
  }
}

const res = spawnSync(binaryPath(), process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error("importstats: failed to run binary: " + res.error.message);
  process.exit(1);
}
if (res.signal) process.kill(process.pid, res.signal);
process.exit(res.status === null ? 1 : res.status);
