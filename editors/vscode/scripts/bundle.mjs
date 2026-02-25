import { context } from 'esbuild';

const watch = process.argv.includes('--watch');

const ctx = await context({
  entryPoints: ['src/extension.ts'],
  outfile: 'dist/extension.js',
  bundle: true,
  format: 'cjs',
  platform: 'node',
  target: 'node18',
  sourcemap: true,
  sourcesContent: false,
  external: ['vscode'],
  logLevel: 'info',
  legalComments: 'none',
});
if (watch) {
  await ctx.watch();
  console.log('thrift-weaver-vscode: esbuild watch started');
} else {
  await ctx.rebuild();
  await ctx.dispose();
}
