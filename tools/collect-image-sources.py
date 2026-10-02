"""Run inside a disposable release image with /sources mounted. Never starts Snowball.

Fetch the exact Debian corresponding-source packages (including Debian build
scripts), retain installed notices, and match Node/noVNC to the shipped versions.
APT authenticates repository metadata and source checksums; missing versions
fail the release rather than creating an incomplete source offer.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import urllib.request

root = Path("/sources")
root.mkdir(exist_ok=True)
sources = Path("/etc/apt/sources.list.d/debian.sources")
text = sources.read_text()
sources.write_text(re.sub(r"^Types: deb$", "Types: deb deb-src", text, flags=re.M))
subprocess.run(["apt-get", "update"], check=True)
query = subprocess.check_output(["dpkg-query", "-W", "-f=${source:Package}\t${source:Version}\n"], text=True)
packages = sorted(set(tuple(line.split("\t")) for line in query.splitlines()))
for name, version in packages:
    if not re.fullmatch(r"[a-z0-9][a-z0-9.+-]*", name) or not version:
        raise RuntimeError("Invalid or missing Debian source metadata")
(root / "debian-source-inventory.json").write_text(json.dumps(packages, indent=2) + "\n")

def download_debian(package):
    name, version = package
    directory = root / "debian" / name / version.replace(":", "_")
    directory.mkdir(parents=True)
    result = subprocess.run(["apt-get", "--yes", "--download-only", "source", f"{name}={version}"], cwd=directory, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if result.returncode:
        raise RuntimeError(f"Missing corresponding source for {name}={version}:\n{result.stdout}")
    if not list(directory.glob("*.dsc")):
        raise RuntimeError(f"No source descriptor for {name}={version}")
    print(f"Corresponding source retained: {name}={version}", flush=True)

with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
    list(pool.map(download_debian, packages))

notices = root / "licenses"
notices.mkdir()
shutil.copytree("/usr/share/doc", notices / "debian", symlinks=True)
shutil.copytree("/usr/share/common-licenses", notices / "common-licenses")
shutil.copytree("/opt/snowball/third-party", notices / "application", symlinks=True)
shutil.copy("/usr/local/LICENSE", notices / "Node-LICENSE")
for filename in ("LICENSE", "THIRD_PARTY_NOTICES.md"):
    shutil.copy(Path("/opt/snowball") / filename, notices / filename)

def download(url, target):
    with urllib.request.urlopen(url, timeout=180) as response, open(target, "wb") as output:
        shutil.copyfileobj(response, output)

node_version = subprocess.check_output(["node", "--version"], text=True).strip()
if not re.fullmatch(r"v22\.\d+\.\d+", node_version):
    raise RuntimeError("Unexpected Node release")
node_file = f"node-{node_version}.tar.xz"
upstream = root / "upstream"
upstream.mkdir()
node_url = f"https://nodejs.org/dist/{node_version}/"
download(node_url + "SHASUMS256.txt", upstream / "NODE-SHASUMS256.txt")
download(node_url + node_file, upstream / node_file)
expected = next(line.split()[0] for line in (upstream / "NODE-SHASUMS256.txt").read_text().splitlines() if line.split()[-1] == node_file)
if hashlib.sha256((upstream / node_file).read_bytes()).hexdigest() != expected:
    raise RuntimeError("Node corresponding-source checksum mismatch")

novnc_file = upstream / "noVNC-1.7.0.tar.gz"
download("https://github.com/novnc/noVNC/archive/refs/tags/v1.7.0.tar.gz", novnc_file)
if hashlib.sha256(novnc_file.read_bytes()).hexdigest() != "b1003a11b6e6e8d8f7f5e5586daae7f8ca651d8aee0aa155ff9ac841c48f52c6":
    raise RuntimeError("noVNC corresponding-source checksum mismatch")
(root / "upstream-versions.json").write_text(json.dumps({"node": node_version, "noVNC": "1.7.0"}, indent=2) + "\n")
print(f"Retained exact sources for {len(packages)} Debian source packages, Node and noVNC.", flush=True)
