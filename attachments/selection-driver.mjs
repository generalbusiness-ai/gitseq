// Drives the REAL selection implementation of actions/upload-artifact pinned at
// 043fb46d1a93c77aae656e7c1c64a875d1fc6a0a (v7.0.1): the exported
// findFilesToUpload from src/shared/search.ts, compiled by the action's own
// tsconfig, resolving the action's own locked @actions/glob 0.6.1.
//
// The action itself calls exactly this function with exactly these two
// arguments (src/upload/upload-artifact.ts:27):
//     findFilesToUpload(inputs.searchPath, inputs.includeHiddenFiles)
// where searchPath is the `path` input verbatim and includeHiddenFiles is the
// `include-hidden-files` input. Nothing here re-implements or mirrors a glob.
import {createHash} from 'node:crypto'
import {readFileSync} from 'node:fs'
import {relative, sep} from 'node:path'

const [, , actionRoot, fixtureRoot, scenariosJSON] = process.argv
const {findFilesToUpload} = await import(
  new URL(`${actionRoot}/lib/shared/search.js`, 'file://').href
)

process.chdir(fixtureRoot)

const sha256 = file => createHash('sha256').update(readFileSync(file)).digest('hex')
const results = []
for (const scenario of JSON.parse(scenariosJSON)) {
  const found = await findFilesToUpload(scenario.path, scenario.includeHiddenFiles)
  results.push({
    label: scenario.label,
    inputs: {path: scenario.path, 'include-hidden-files': scenario.includeHiddenFiles},
    rootDirectory: relative(fixtureRoot, found.rootDirectory) || '.',
    count: found.filesToUpload.length,
    selected: found.filesToUpload
      .map(f => ({path: relative(fixtureRoot, f).split(sep).join('/'), sha256: sha256(f)}))
      .sort((a, b) => (a.path < b.path ? -1 : 1))
  })
}
process.stdout.write(JSON.stringify(results, null, 2) + '\n')
