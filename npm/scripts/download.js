#!/usr/bin/env node
// Downloads the platform-specific skills-manager binary from GitHub Releases
// into this package's bin/ directory at install time. The package version is
// kept in lockstep with the release tag by the publish workflow.
"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
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
  // No windows/arm64 build is released; fail clearly instead of 404-ing.
  if (goos === "windows" && arch === "arm64") {
    throw new Error("windows/arm64 is not supported; use the linux or macOS build, or build from source");
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

  extract(archivePath, ext, binDir);
  fs.rmSync(archivePath, { force: true });
  const binPath = path.join(binDir, `skills-manager${exe}`);
  if (!fs.existsSync(binPath)) throw new Error("binary not found after extraction");
  fs.chmodSync(binPath, 0o755);
  process.stdout.write(`skills-manager: installed ${binPath}\n`);
}

// extract unpacks the archive into binDir. tar.gz uses tar (present on macOS,
// Linux, and Windows 10+). zip (Windows) uses PowerShell Expand-Archive, which
// is built into Windows 10+, falling back to bsdtar.
function extract(archivePath, ext, binDir) {
  if (ext === "tar.gz") {
    execFileSync("tar", ["-xzf", archivePath, "-C", binDir]);
    return;
  }
  const ps = (p) => "'" + String(p).replace(/'/g, "''") + "'";
  try {
    execFileSync(
      "powershell",
      ["-NoProfile", "-NonInteractive", "-Command",
        `Expand-Archive -Path ${ps(archivePath)} -DestinationPath ${ps(binDir)} -Force`],
      { stdio: "ignore" }
    );
  } catch (_) {
    execFileSync("tar", ["-xf", archivePath, "-C", binDir]); // bsdtar handles zip on modern Windows
  }
}

main().catch((err) => {
  process.stderr.write(`skills-manager install failed: ${err.message}\n`);
  process.exit(1);
});
