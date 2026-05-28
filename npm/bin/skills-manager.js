#!/usr/bin/env node
// Launcher: exec the downloaded native binary, forwarding args and exit code.
"use strict";

const path = require("path");
const fs = require("fs");
const { spawnSync } = require("child_process");

const exe = process.platform === "win32" ? "skills-manager.exe" : "skills-manager";
const binPath = path.join(__dirname, exe);

if (!fs.existsSync(binPath)) {
  process.stderr.write(
    "skills-manager binary missing — reinstall the package (the postinstall step downloads it).\n"
  );
  process.exit(1);
}

const result = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  process.stderr.write(`skills-manager: ${result.error.message}\n`);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
