[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Port,
    [string]$BuildDir = (Join-Path $PSScriptRoot '..\firmware\esp32-s3-audio\build-minis-ghost')
)

$ErrorActionPreference = 'Stop'

# This is the direct-USB counterpart to flash-esp32.sh.  Keep its command
# tail deliberately fixed: Wi-Fi, pairing and device identity live in NVS at
# 0x9000 and are never an allowed target.
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\firmware\esp32-s3-audio')).Path
$resolvedBuild = (Resolve-Path $BuildDir).Path
if (-not $resolvedBuild.StartsWith($projectRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Firmware build directory must remain inside $projectRoot"
}

$flashArgs = Join-Path $resolvedBuild 'flash_args'
$expected = @(
    '0x0 bootloader/bootloader.bin',
    '0x8000 partition_table/partition-table.bin',
    '0x10000 snowball_speaker.bin',
    '0x310000 srmodels/srmodels.bin'
)
$actual = Get-Content -LiteralPath $flashArgs
if ($actual -match '(^|\s)0x9000(\s|$)|(^|\s)nvs(\s|$)') {
    throw 'The selected flash arguments address NVS; refusing this recovery path.'
}
foreach ($entry in $expected) {
    if ($actual -notcontains $entry) {
        throw "The selected build has unexpected flash offsets; missing $entry"
    }
}
foreach ($relative in @('bootloader\bootloader.bin', 'partition_table\partition-table.bin', 'snowball_speaker.bin', 'srmodels\srmodels.bin')) {
    $image = Join-Path $resolvedBuild $relative
    if (-not (Test-Path -LiteralPath $image -PathType Leaf) -or (Get-Item -LiteralPath $image).Length -eq 0) {
        throw "The approved firmware image is missing: $image"
    }
}

$esptool = 'C:\Espressif\tools\python\v5.5.5\venv\Scripts\python.exe'
if (-not (Test-Path -LiteralPath $esptool -PathType Leaf)) {
    throw 'ESP-IDF 5.5.5 Python environment is unavailable.'
}

$bootloader = Join-Path $resolvedBuild 'bootloader\bootloader.bin'
$partition = Join-Path $resolvedBuild 'partition_table\partition-table.bin'
$application = Join-Path $resolvedBuild 'snowball_speaker.bin'
$models = Join-Path $resolvedBuild 'srmodels\srmodels.bin'
& $esptool -m esptool --chip esp32s3 --port $Port -b 460800 --before default_reset --after hard_reset write_flash `
    --flash_mode dio --flash_freq 80m --flash_size 16MB `
    0x0 $bootloader 0x8000 $partition 0x10000 $application 0x310000 $models
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Output 'flash_verified_by_esptool; NVS 0x9000 was not addressed'
