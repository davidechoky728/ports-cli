#!/usr/bin/env node
const { spawnSync } = require("child_process");
const path = require("path");
const fs = require("fs");

const bin = path.join(__dirname, "ports");

if (!fs.existsSync(bin)) {
  console.error(
    "[ports-cli] binary missing. Re-run: npm install -g @erdemyilmaz/ports-cli"
  );
  process.exit(1);
}

const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
process.exit(result.status ?? 1);
