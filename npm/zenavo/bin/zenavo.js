#!/usr/bin/env node
// Runs the zenavo binary that npm installed for this machine: one of this package's optional dependencies,
// @zenavo/cli-<platform>-<arch>, each made for a single system. Arguments, input and output go straight through, and
// zenavo's exit code becomes this one's. Nothing runs at install time.
'use strict'

const { spawnSync } = require('node:child_process')
const fs = require('node:fs')

const pkg = `@zenavo/cli-${process.platform}-${process.arch}`
const exe = process.platform === 'win32' ? 'zenavo.exe' : 'zenavo'

let bin
try {
  bin = require.resolve(`${pkg}/bin/${exe}`)
} catch {
  console.error(`zenavo: ${pkg} is not installed, so there is no zenavo for ${process.platform} on ${process.arch} here.`)
  console.error('Reinstall without --omit=optional or --no-optional, or install zenavo another way: https://docs.zenavo.app/cli')
  process.exit(1)
}

if (process.platform !== 'win32') {
  // npm keeps the executable bit when it unpacks, but some tools that copy node_modules do not.
  try {
    fs.accessSync(bin, fs.constants.X_OK)
  } catch {
    try { fs.chmodSync(bin, 0o755) } catch { /* reported by the spawn below */ }
  }
}

const result = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit', windowsHide: true })
if (result.error) {
  console.error(`zenavo: could not run ${bin}: ${result.error.message}`)
  process.exit(1)
}
if (result.signal) {
  process.kill(process.pid, result.signal)
}
process.exit(result.status ?? 1)
