#!/usr/bin/env node
// Builds the npm packages of a release from the binaries in dist/ (make dist):
//
//   node scripts/npm-packages.mjs v0.2.0 dist out/npm
//
// out/npm/cli-<platform>-<arch> is @zenavo/cli-<platform>-<arch>, holding one binary and limited by os and cpu to the
// system it runs on. out/npm/zenavo is zenavo itself: a small launcher (npm/zenavo) that depends on every platform
// package optionally, so npm installs only the one for this machine. Publish the platform packages first.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Go's names in dist/, and Node's for the same system (process.platform, process.arch).
export const platforms = [
  { goos: 'darwin', goarch: 'arm64', os: 'darwin', cpu: 'arm64' },
  { goos: 'darwin', goarch: 'amd64', os: 'darwin', cpu: 'x64' },
  { goos: 'linux', goarch: 'arm64', os: 'linux', cpu: 'arm64' },
  { goos: 'linux', goarch: 'amd64', os: 'linux', cpu: 'x64' },
  { goos: 'windows', goarch: 'arm64', os: 'win32', cpu: 'arm64' },
  { goos: 'windows', goarch: 'amd64', os: 'win32', cpu: 'x64' },
]

const common = {
  license: 'Apache-2.0',
  homepage: 'https://docs.zenavo.app/cli',
  repository: { type: 'git', url: 'git+https://github.com/Zenavo-App/zenavo-cli.git' },
  bugs: 'https://github.com/Zenavo-App/zenavo-cli/issues',
}

function writeJSON(file, value) {
  fs.writeFileSync(file, JSON.stringify(value, null, 2) + '\n')
}

export function build(tag, dist, out) {
  if (!/^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(tag)) throw new Error(`not a version tag: ${tag}`)
  const version = tag.slice(1)
  const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
  fs.rmSync(out, { recursive: true, force: true })

  const optional = {}
  for (const p of platforms) {
    const name = `@zenavo/cli-${p.os}-${p.cpu}`
    const exe = p.goos === 'windows' ? 'zenavo.exe' : 'zenavo'
    const from = path.join(dist, `zenavo_${p.goos}_${p.goarch}${p.goos === 'windows' ? '.exe' : ''}`)
    if (!fs.existsSync(from)) throw new Error(`missing ${from}`)
    const dir = path.join(out, `cli-${p.os}-${p.cpu}`)
    fs.mkdirSync(path.join(dir, 'bin'), { recursive: true })
    fs.copyFileSync(from, path.join(dir, 'bin', exe))
    fs.chmodSync(path.join(dir, 'bin', exe), 0o755)
    writeJSON(path.join(dir, 'package.json'), {
      name, version, description: `The zenavo binary for ${p.os} on ${p.cpu}. Install zenavo instead.`,
      ...common, os: [p.os], cpu: [p.cpu], files: ['bin'], preferUnplugged: true,
    })
    fs.writeFileSync(path.join(dir, 'README.md'), `# ${name}\n\nThe [zenavo](https://www.npmjs.com/package/zenavo) binary for ${p.os} on ${p.cpu}. Install \`zenavo\`, which picks the right one for you.\n`)
    optional[name] = version
  }

  const main = path.join(out, 'zenavo')
  fs.cpSync(path.join(root, 'npm', 'zenavo'), main, { recursive: true })
  fs.chmodSync(path.join(main, 'bin', 'zenavo.js'), 0o755)
  fs.copyFileSync(path.join(root, 'LICENSE'), path.join(main, 'LICENSE'))
  writeJSON(path.join(main, 'package.json'), {
    name: 'zenavo', version, description: 'Deploy and operate projects on your own servers with Zenavo',
    keywords: ['zenavo', 'deploy', 'paas', 'cli', 'self-hosted'],
    ...common, bin: { zenavo: 'bin/zenavo.js' }, files: ['bin', 'LICENSE'], engines: { node: '>=18' },
    optionalDependencies: optional,
  })
  return { version, packages: [...platforms.map(p => path.join(out, `cli-${p.os}-${p.cpu}`)), main] }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [tag, dist, out] = process.argv.slice(2)
  if (!tag || !dist || !out) {
    console.error('usage: node scripts/npm-packages.mjs <tag> <dist> <out>')
    process.exit(2)
  }
  for (const dir of build(tag, dist, out).packages) console.log(dir)
}
