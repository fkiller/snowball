# Development glob adapter

The npm override routes the two `fast-glob` consumers (Next's lint root-directory
discovery and Vite's dynamic-import expansion) to this small adapter. It uses
`tinyglobby` without `micromatch` or `braces`, removing the unpatched
[GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm)
dependency from the installed tree. Runtime dependencies are unaffected.

This is a scoped compatibility layer, not a complete fast-glob implementation.
Unsupported options fail explicitly. Directory expansion is disabled as required
by [tinyglobby's migration guidance](https://superchupu.dev/tinyglobby/documentation).
The fixture tests exercise the upstream call shapes, brace extension lists,
ignores, absolute paths and both CommonJS and ESM imports. Keep the override
until those upstream tools remove their vulnerable dependency, then remove this
adapter and its tests together. Do not suppress audit findings.

The project MIT license applies to this adapter; tinyglobby and its dependencies
retain their own licenses collected by the existing notice collector.
