'use strict';

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const packageRoot = path.resolve(__dirname, '..', '..');
const packageJson = JSON.parse(
  fs.readFileSync(path.join(packageRoot, 'package.json'), 'utf8'),
);
const distDirectory = path.join(packageRoot, 'npm', 'dist');
const targets = [
  ['linux', 'amd64'],
  ['linux', 'arm64'],
  ['darwin', 'amd64'],
  ['darwin', 'arm64'],
  ['windows', 'amd64'],
  ['windows', 'arm64'],
];

fs.rmSync(distDirectory, { recursive: true, force: true });
fs.mkdirSync(distDirectory, { recursive: true });

for (const [goos, goarch] of targets) {
  const targetDirectory = path.join(distDirectory, `${goos}-${goarch}`);
  const binaryName = goos === 'windows' ? 'spacedock.exe' : 'spacedock';
  const outputPath = path.join(targetDirectory, binaryName);

  fs.mkdirSync(targetDirectory, { recursive: true });

  const result = spawnSync(
    'go',
    [
      'build',
      '-buildvcs=false',
      '-trimpath',
      '-ldflags',
      `-s -w -X github.com/starlove7/spacedock/internal/buildinfo.Version=${packageJson.version}`,
      '-o',
      outputPath,
      './cmd/spacedock',
    ],
    {
      cwd: packageRoot,
      env: {
        ...process.env,
        GOOS: goos,
        GOARCH: goarch,
        CGO_ENABLED: '0',
      },
      stdio: 'inherit',
      shell: false,
    },
  );

  if (result.error) {
    console.error(`Unable to build ${goos}-${goarch}: ${result.error.message}`);
    process.exit(1);
  }

  if (result.status !== 0) {
    console.error(`Unable to build ${goos}-${goarch}: go exited with status ${result.status}`);
    process.exit(1);
  }

  if (goos !== 'windows') {
    fs.chmodSync(outputPath, 0o755);
  }
}
