[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9_][A-Za-z0-9_.@-]*$')][string]$GateHost,
    [ValidateSet('build', 'install', 'status')][string]$Action = 'status',
    [Parameter(Mandatory = $true)][ValidatePattern('^/[A-Za-z0-9_./-]+$')][string]$RemotePath,
    [ValidatePattern('^[0-9.]*$')][string]$LanIp = ''
)
$ErrorActionPreference = 'Stop'
if ($Action -eq 'install' -and -not $LanIp) { throw '-LanIp must be the private IPv4 assigned to the Linux VM/host.' }
$command = "cd '$RemotePath' && SNOWBALL_LAN_IP='$LanIp' sh tools/gate-start.sh $Action"
& ssh $GateHost $command
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
