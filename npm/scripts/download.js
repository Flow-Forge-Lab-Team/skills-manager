#!/usr/bin/env node
// Downloads the platform-specific skills-manager binary from GitHub Releases
// into this package's bin/ directory at install time. The package version is
// kept in lockstep with the release tag by the publish workflow.
"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const zlib = require("zlib");
const { execFileSync } = require("child_process");

const REPO = "Flow-Forge-Lab-Team/skills-manager";
const pkg = require("../package.json");

function target() {
  const platform = os.platform();
  const arch = os.arch() === "x64" ? "amd64" : os.arch();
  const goos = platform === "win32" ? "windows" : platform; // darwin | linux | windows
  if (!["darwin", "linux", "windows"].includes(goos)) {
    throw new Error(`unsupported platform: ${platform}`);
  }
  if (!["amd64", "arm64"].includes(arch)) {
    throw new Error(`unsupported architecture: ${os.arch()}`);
  }
  const ext = goos === "windows" ? "zip" : "tar.gz";
  return { goos, arch, ext, exe: goos === "windows" ? ".exe" : "" };
}

function download(url, dest, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) return reject(new Error("too many redirects"));
    https.get(url, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        return resolve(download(res.headers.location, dest, redirects + 1));
      }
      if (res.statusCode !== 200) {
        return reject(new Error(`download failed: HTTP ${res.statusCode} for ${url}`));
      }
      const file = fs.createWriteStream(dest);
      res.pipe(file);
      file.on("finish", () => file.close(resolve));
      file.on("error", reject);
    }).on("error", reject);
  });
}

async function main() {
  const { goos, arch, ext, exe } = target();
  const version = pkg.version && pkg.version !== "0.0.0" ? `v${pkg.version}` : "latest";
  const base =
    version === "latest"
      ? `https://github.com/${REPO}/releases/latest/download`
      : `https://github.com/${REPO}/releases/download/${version}`;
  const asset = `skills-manager_${goos}_${arch}.${ext}`;
  const binDir = path.join(__dirname, "..", "bin");
  fs.mkdirSync(binDir, { recursive: true });
  const archivePath = path.join(binDir, asset);

  process.stdout.write(`skills-manager: downloading ${base}/${asset}\n`);
  await download(`${base}/${asset}`, archivePath);

  // Extract just the binary. tar.gz via zlib + tar is heavy; shell out to the
  // platform's tar/unzip which is universally available on the supported OSes.
  if (ext === "tar.gz") {
    execFileSync("tar", ["-xzf", archivePath, "-C", binDir]);
  } else {
    execFileSync("unzip", ["-o", archivePath, "-d", binDir]);
  }
  fs.rmSync(archivePath, { force: true });
  const binPath = path.join(binDir, `skills-manager${exe}`);
  if (!fs.existsSync(binPath)) throw new Error("binary not found after extraction");
  fs.chmodSync(binPath, 0o755);
  process.stdout.write(`skills-manager: installed ${binPath}\n`);
  void zlib; // reserved for a future pure-JS extraction path
}

main().catch((err) => {
  process.stderr.write(`skills-manager install failed: ${err.message}\n`);
  process.exit(1);
});
