const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const root = path.resolve(__dirname, '../..');
const packageJsonPath = path.join(root, 'package.json');

function readPackageJson() {
  return JSON.parse(fs.readFileSync(packageJsonPath, 'utf8'));
}

test('package metadata and allowlist match the distribution contract', () => {
  const pkg = readPackageJson();

  assert.equal(pkg.name, '@starlove7/spacedock');
  assert.equal(pkg.version, '0.1.0');
  assert.equal(pkg.bin?.spacedock, 'npm/bin/spacedock.js');
  assert.equal(pkg.publishConfig?.access, 'public');
  assert.ok(Array.isArray(pkg.files));

  for (const entry of [
    'npm/bin/spacedock.js',
    'npm/dist/',
    'README.md',
    'README.ko.md',
    'config.example.yaml',
  ]) {
    assert.ok(pkg.files.includes(entry), `files allowlist missing ${entry}`);
  }

  assert.equal(Object.hasOwn(pkg, 'dependencies'), false);
});

test('all release binaries exist and are nonempty', () => {
  for (const relativePath of [
    'npm/dist/linux-amd64/spacedock',
    'npm/dist/linux-arm64/spacedock',
    'npm/dist/darwin-amd64/spacedock',
    'npm/dist/darwin-arm64/spacedock',
    'npm/dist/windows-amd64/spacedock.exe',
    'npm/dist/windows-arm64/spacedock.exe',
  ]) {
    const filePath = path.join(root, relativePath);
    assert.ok(fs.existsSync(filePath), `missing binary ${relativePath}`);
    assert.ok(fs.statSync(filePath).size > 0, `empty binary ${relativePath}`);
  }
});

test('host launcher reports the package version', () => {
  const pkg = readPackageJson();
  const result = spawnSync(process.execPath, [path.join(root, 'npm/bin/spacedock.js'), 'version'], {
    cwd: root,
    encoding: 'utf8',
  });

  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), pkg.version);
});
