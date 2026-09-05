import io, json, pathlib, subprocess, tarfile, tempfile
repo = pathlib.Path('/Users/hughpyle/play/gitseq-worktrees/remove-local-jsonata-spike')
head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
archive = subprocess.check_output(['git', 'archive', head], cwd=repo)
results = []
for name in ['restored-source', 'unused-module']:
    with tempfile.TemporaryDirectory(prefix='gitseq-i9-mutant-') as directory:
        root = pathlib.Path(directory)
        with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
            tar.extractall(root, filter='data')
        if name == 'restored-source':
            path = root / 'spike/jsonataddl/restored.go'
            path.parent.mkdir(parents=True)
            path.write_text('package jsonataddl\n')
            expected = 'removed inventory surface spike/jsonataddl is present'
        else:
            mod = root / 'go.mod'
            mod.write_text(mod.read_text() + '\nrequire github.com/jsonata-go/jsonata v0.0.0-20250709164031-599f35f32e5f\n')
            old = subprocess.check_output(['git', 'show', head+'^:go.sum'], cwd=repo, text=True)
            with (root / 'go.sum').open('a') as sums:
                sums.write('\n' + '\n'.join(line for line in old.splitlines() if line.startswith('github.com/jsonata-go/jsonata ')) + '\n')
            expected = 'unused JSONata evaluator remains in module graph'
        run = subprocess.run(['go', 'test', './internal/boundary', '-run', 'TestInventoryStaysOutsideGitseq', '-count=1'], cwd=root, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if run.returncode == 0 or expected not in run.stdout:
            raise RuntimeError(name + ': mutant did not fail the boundary: ' + run.stdout)
        results.append({'mutant': name, 'exit_code': run.returncode, 'expected_failure': expected, 'output': run.stdout})
result = {'head': head, 'mutants': results}
pathlib.Path('/tmp/gitseq-i9-mutations.json').write_text(json.dumps(result, indent=2))
print(json.dumps(result, indent=2))
