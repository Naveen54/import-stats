#!/usr/bin/env node
"use strict";

// Thin launcher: runs the prebuilt importstats binary for this OS/CPU, which is
// bundled in this package under bin/<platform>-<arch>/.

const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

function binaryPath() {
  if (process.env.IMPORTSTATS_BINARY) return process.env.IMPORTSTATS_BINARY;

  const target = process.platform + "-" + process.arch;
  const exe = process.platform === "win32" ? "importstats.exe" : "importstats";
  const file = path.join(__dirname, target, exe);
  if (!fs.existsSync(file)) {
    console.error(
      "importstats: no prebuilt binary for " + target + ".\n" +
        "Download a binary or build from source: https://github.com/Naveen54/import-stats"
    );
    process.exit(1);
  }
  return file;
}

const res = spawnSync(binaryPath(), process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error("importstats: failed to run binary: " + res.error.message);
  process.exit(1);
}
if (res.signal) process.kill(process.pid, res.signal);
process.exit(res.status === null ? 1 : res.status);
