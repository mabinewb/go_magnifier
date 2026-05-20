[CmdletBinding()]
param(
    [switch]$ForceGoDownload,
    [switch]$Rebuild
)

$ErrorActionPreference = 'Stop'

$ProjectRoot = Split-Path $PSScriptRoot -Parent
$ExePath = Join-Path $ProjectRoot 'dist\GoMagnifier.exe'

if ($Rebuild -or -not (Test-Path $ExePath)) {
    & (Join-Path $PSScriptRoot 'build.ps1') -ForceGoDownload:$ForceGoDownload
}

Start-Process -FilePath $ExePath -WorkingDirectory $ProjectRoot
Write-Host ("Started: {0}" -f $ExePath)