#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const https = require("https");

const pkg = require("../package.json");
const { platform, arch } = process;

if (platform !== "darwin") {
  console.error(
    `[ports-cli] only macOS is supported (detected platform: ${platform}). Skipping binary download.`
  );
  process.exit(0);
}

const archMap = { arm64: "arm64", x64: "amd64" };
const goArch = archMap[arch];
if (!goArch) {
  console.error(`[ports-cli] unsupported arch: ${arch}`);
  process.exit(1);
}

const url = `https://github.com/erdemylmaz/ports-cli/releases/download/v${pkg.version}/ports-darwin-${goArch}`;
const out = path.join(__dirname, "ports");

function download(u, dest, redirects = 0) {
  if (redirects > 5) {
    console.error("[ports-cli] too many redirects");
    process.exit(1);
  }
  https
    .get(u, { headers: { "User-Agent": "ports-cli-installer" } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        return download(res.headers.location, dest, redirects + 1);
      }
      if (res.statusCode !== 200) {
        console.error(`[ports-cli] download failed: HTTP ${res.statusCode} for ${u}`);
        process.exit(1);
      }
      const file = fs.createWriteStream(dest, { mode: 0o755 });
      res.pipe(file);
      file.on("finish", () => {
        file.close(() => {
          fs.chmodSync(dest, 0o755);
          console.log(`[ports-cli] installed binary at ${dest}`);
        });
      });
    })
    .on("error", (err) => {
      console.error(`[ports-cli] download error: ${err.message}`);
      process.exit(1);
    });
}

download(url, out);
