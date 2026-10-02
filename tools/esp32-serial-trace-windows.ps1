[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Port,
    [Parameter(Mandatory = $true)]
    [string]$LogPath,
    [int]$DurationSeconds = 900
)

$ErrorActionPreference = 'Stop'
$deadline = [DateTime]::UtcNow.AddSeconds($DurationSeconds)
$directory = Split-Path -Parent $LogPath
if ($directory) {
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
}

while ([DateTime]::UtcNow -lt $deadline) {
    $serial = $null
    try {
        $serial = [System.IO.Ports.SerialPort]::new($Port, 115200, 'None', 8, 'One')
        $serial.ReadTimeout = 1000
        $serial.DtrEnable = $false
        $serial.RtsEnable = $false
        $serial.Open()
        "$(Get-Date -Format o) trace_connected port=$Port" | Add-Content -LiteralPath $LogPath
        while ([DateTime]::UtcNow -lt $deadline -and $serial.IsOpen) {
            try {
                $line = $serial.ReadLine()
                if ($line) {
                    "$(Get-Date -Format o) $line" | Add-Content -LiteralPath $LogPath
                }
            } catch [System.TimeoutException] {
                continue
            }
        }
    } catch {
        "$(Get-Date -Format o) trace_reconnect reason=$($_.Exception.Message)" | Add-Content -LiteralPath $LogPath
        Start-Sleep -Seconds 2
    } finally {
        if ($serial) {
            $serial.Dispose()
        }
    }
}
