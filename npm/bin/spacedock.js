#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const platformNames = {
  linux: 'linux',
  darwin: 'darwin',
  win32: 'windows',
};
const architectureNames = {
  x64: 'amd64',
  arm64: 'arm64',
};

const goos = platformNames[process.platform];
const goarch = architectureNames[process.arch];

if (!goos || !goarch) {
  console.error(
    'Unsupported platform. Supported platforms: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64, windows-arm64.',
  );
  process.exit(1);
}

const binaryName = goos === 'windows' ? 'spacedock.exe' : 'spacedock';
const binaryPath = path.resolve(__dirname, '..', 'dist', `${goos}-${goarch}`, binaryName);

if (!fs.existsSync(binaryPath)) {
  console.error(`SpaceDock binary not found: ${binaryPath}`);
  process.exit(1);
}

const result = spawnSync(binaryPath, process.argv.slice(2), {
  stdio: 'inherit',
  shell: false,
  windowsHide: false,
});

if (result.error) {
  console.error(`Unable to start SpaceDock: ${result.error.message}`);
  process.exit(1);
}

if (result.signal) {
  if (process.platform !== 'win32') {
    try {
      process.kill(process.pid, result.signal);
    } catch (_error) {
      process.exit(1);
    }
  } else {
    process.exit(1);
  }
}

process.exit(result.status === null ? 1 : result.status);
