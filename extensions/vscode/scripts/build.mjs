import { build } from 'esbuild';
import { mkdir, copyFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

await mkdir(new URL('../dist/', import.meta.url), { recursive: true });
await build({ entryPoints: [fileURLToPath(new URL('../src/extension.ts', import.meta.url))], outfile: fileURLToPath(new URL('../dist/extension.js', import.meta.url)), bundle: true, platform: 'node', target: 'node18', external: ['vscode','./citation.mjs'], sourcemap: false, minify: true });
await build({ entryPoints: [fileURLToPath(new URL('../src/protocol.ts', import.meta.url))], outfile: fileURLToPath(new URL('../dist/protocol.cjs', import.meta.url)), bundle: true, platform: 'node', target: 'node18', sourcemap: false });
await copyFile(new URL('../../../internal/transport/http/web/citation.mjs', import.meta.url), new URL('../dist/citation.mjs', import.meta.url));
