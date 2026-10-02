"""Retain framework third-party notices from the exact SDK used to compile."""
from pathlib import Path
import re
import shutil
import sys

sdk, output = map(Path, sys.argv[1:3])
output.mkdir(parents=True, exist_ok=True)
shutil.copy(sdk / "LICENSE", output / "LICENSE")
for source in (sdk / "components").rglob("*"):
    if source.is_file() and re.match(r"^(license|copying|notice|copyright)([._-]|$)", source.name, re.I):
        target = output / source.relative_to(sdk)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(source, target)
print(f"SDK notices retained: {len(list(output.rglob('*')))} entries")
