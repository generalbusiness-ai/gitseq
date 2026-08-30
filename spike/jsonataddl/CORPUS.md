# JSONata compatibility and determinism corpus

The focused corpus compares the pinned Go port with the JavaScript JSONata
2.0.6 reference on every test run. The reference source is carried by the
pinned `github.com/jsonata-go/jsonata` module; the test locates that exact Go
module and runs [`testdata/reference.js`](testdata/reference.js) with Node. The
checked results in [`testdata/compatibility.json`](testdata/compatibility.json)
therefore fail if either the fixture or the live reference result moves.

The portable expressions agree in both engines and repeat identically in the
Go port:

- `event.qty + rows.stock_row.available`
- `$map([1,2,3], function($v){$v*$v})`
- `[1..5]`
- `$sum([0.1,0.2])`
- `$round(2.675,2)`

Four expressions expose a reference difference and order dependence. JSONata
2.0.6 preserves the object literal's insertion order. The Go port ranges over
a Go map, so repeated evaluations return both orders:

- `$keys({"b":1,"a":2})`
- `$each({"b":1,"a":2}, function($v,$k){$k})`
- `$spread({"b":1,"a":2})`
- `{"b":1,"a":2}.*`

The spike narrows the fold profile rather than blessing either result: profile
loading rejects `$keys`, `$each`, `$spread`, and object wildcard or descendant
traversal. The fold version advances to `jsonata-v206-sqlite-spike@1` because
that admitted language is part of fold identity.

The reference also demonstrates three environment-dependent expressions:
`$now()`, `$random()`, and `$shuffle([1,2,3,4])`. The profile rejects all
three. `$millis` and `$eval` remain rejected by the same closed profile even
though the focused corpus does not execute them.

This is a targeted compatibility corpus for the inventory fold language, not
a claim that the whole JSONata language agrees. It is useful because it runs
the real reference, exercises arithmetic, ranges, higher-order evaluation,
decimal behaviour, ambient inputs, and all object-to-sequence operations the
spike excludes. Deterministic evaluation-step and allocation bounds, and a
complete safe-number contract, remain production blockers.

Run it with:

```text
go test ./spike/jsonataddl -run TestJSONataCompatibilityAndDeterminismCorpus -count=1 -v
```
