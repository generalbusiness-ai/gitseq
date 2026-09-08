#!/usr/bin/env python3
"""Replay the receipt regression and its isolated compiled omission control."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile

checkout = Path(sys.argv[1]).resolve()
expected_head = "fd6b4c278e105dca532b9eaa4e65337bc2d53e22"
assert subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip() == expected_head
source = checkout / "cmd/gs/succession.go"
original = source.read_bytes()
marker = "\t// Also on the resume path, which reaches here without validateMerge:"
text = original.decode()
assert text.count(marker) == 1
fault = """\tvar retirements map[string]string
\tif err := json.Unmarshal([]byte(receipt.Retirements), &retirements); err != nil { return err }
\tif err := mergeplan.ValidateReach(snapshot.Projection, mergeplan.Succession{Retire: retirements}, receipt.Approval, workspace.View().Actors[actor].Fingerprint); err != nil { return err }
"""
out = Path(tempfile.mkdtemp(prefix="receipt-resume-omission-"))
mutant = out / "succession.go"
mutant.write_text(text.replace(marker, fault + marker))
overlay = out / "overlay.json"
overlay.write_text(json.dumps({"Replace": {str(source): str(mutant)}}))
tests = "TestMergeResumeAppendsA(HistoricalWider|SealedExactPath)ReceiptWithoutReplanningOrRemerging$"
for name, flags, expected in [("baseline", [], 0), ("mutant", ["-overlay", str(overlay)], 1), ("restored", [], 0)]:
    command = ["go", "test", *flags, "./cmd/gs", "-run", tests, "-count=1", "-v"]
    result = subprocess.run(command, cwd=checkout, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (out / (name + ".log")).write_text(result.stdout)
    assert result.returncode == expected, (name, result.returncode, result.stdout)
    if name == "mutant":
        assert "--- FAIL: TestMergeResumeAppendsAHistoricalWiderReceipt" in result.stdout
        assert "--- PASS: TestMergeResumeAppendsASealedExactPathReceipt" in result.stdout
        assert "outside the reviewed paths" in result.stdout
    assert source.read_bytes() == original
print(out)
