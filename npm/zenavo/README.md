# zenavo

The [Zenavo](https://zenavo.app) command line: deploy and operate projects on your own servers from a terminal or CI.

```sh
npm install -g zenavo
zenavo auth login
```

Or run it without installing: `npx zenavo deploy`.

npm installs only the binary for your system, from one of the `@zenavo/cli-<platform>-<arch>` packages, and nothing runs at install time. Each release is built and published by the [zenavo-cli](https://github.com/Zenavo-App/zenavo-cli) repository's CI with npm provenance, so `npm audit signatures` can check where it came from.

Other ways to install, and every command: [docs.zenavo.app/cli](https://docs.zenavo.app/cli).
