import terser from '@rollup/plugin-terser';

// Output goes into the Go package directory so `go:embed` (which forbids
// `..` paths) can pick it up directly. The built artifact is committed
// alongside the source so the binary builds offline without Node.
export default {
  input: 'src/tracker.js',
  output: {
    file: '../internal/tracker/dist/tracker.js',
    format: 'iife',
    sourcemap: false,
    // Drops the rollup-emitted "use strict" wrapper directive (~14 bytes
    // pre-gzip). The IIFE has no eval / no globals / no `this` reliance,
    // so non-strict has no observable behavior change here.
    strict: false,
  },
  plugins: [
    terser({
      compress: { passes: 3, drop_console: true, drop_debugger: true },
      mangle: { toplevel: true },
      format: { comments: false, ecma: 2017 },
    }),
  ],
};
