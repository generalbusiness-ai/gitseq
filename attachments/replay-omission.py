#!/usr/bin/env python3
"""Run the proper-prefix recovery test and the compiled mixed-outcome fault."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile

checkout = Path(sys.argv[1]).resolve()
assert subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip() == "03239a872ba339216780006622bbd0e8df0d7fa5"
source = checkout / "cmd/gs/main.go"
original = source.read_bytes()
text = original.decode()
marker = "\t\treport.Acts[position].Event = submission.Record.ID"
assert text.count(marker) == 1
fault = """\t\tif !submission.Result.Replay && report.Replayed > 0 {
\t\t\treturn report, batchFail("submit", "mutation rejects mixed replay and newly landed suffix")
\t\t}
"""
out = Path(tempfile.mkdtemp(prefix="batch-prefix-omission-"))
mutant = out / "main.go"
mutant.write_text(text.replace(marker, fault + marker))
overlay = out / "overlay.json"
overlay.write_text(json.dumps({"Replace": {str(source): str(mutant)}}))
tests = "TestBatch(ResumesADurablePrefixAndReplaysTheCompletedChain|PreflightRefusalThenFullLandingAndReplay)$"
for phase, flags, expected in [("baseline", [], 0), ("mutant", ["-overlay", str(overlay)], 1), ("restored", [], 0)]:
    command = ["go", "test", *flags, "./cmd/gs", "-run", tests, "-count=1", "-v"]
    result = subprocess.run(command, cwd=checkout, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (out / (phase + ".log")).write_text(result.stdout)
    assert result.returncode == expected, (phase, result.returncode, result.stdout)
    if phase == "mutant":
        assert "--- PASS: TestBatchPreflightRefusalThenFullLandingAndReplay" in result.stdout
        assert "--- FAIL: TestBatchResumesADurablePrefixAndReplaysTheCompletedChain" in result.stdout
        assert "mutation rejects mixed replay and newly landed suffix" in result.stdout
    assert source.read_bytes() == original
print(out)
