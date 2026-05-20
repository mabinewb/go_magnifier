[CmdletBinding()]
param(
    [switch]$ForceGoDownload,
    [switch]$Clean
)

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'bootstrap-go.ps1') -ForceDownload:$ForceGoDownload

function Invoke-NativeOrThrow {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter()][string[]]$Arguments = @()
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw ("Command failed with exit code {0}: {1} {2}" -f $LASTEXITCODE, $FilePath, ($Arguments -join ' '))
    }
}

$ProjectRoot = Split-Path $PSScriptRoot -Parent
$DistDir = Join-Path $ProjectRoot 'dist'
$ExePath = Join-Path $DistDir 'GoMagnifier.exe'
$ManifestSource = Join-Path $ProjectRoot 'packaging\GoMagnifier.exe.manifest'
$ManifestTarget = Join-Path $DistDir 'GoMagnifier.exe.manifest'
$IconSource = Join-Path $ProjectRoot 'packaging\GoMagnifier.ico'
$IconTarget = Join-Path $DistDir 'GoMagnifier.ico'
$IconGenerator = Join-Path $PSScriptRoot 'generate-icon.ps1'
$GoBinDir = Join-Path (& $script:GoExe env GOPATH) 'bin'
$RsrcExe = Join-Path $GoBinDir 'rsrc.exe'
$MainPackageDir = Join-Path $ProjectRoot 'cmd\magnifier'
$ResourceOutput = Join-Path $MainPackageDir 'rsrc_windows_amd64.syso'
$LegacyResourceOutput = Join-Path $ProjectRoot 'rsrc_windows_amd64.syso'

if (-not (Test-Path $RsrcExe)) {
    Invoke-NativeOrThrow -FilePath $script:GoExe -Arguments @('install', 'github.com/akavel/rsrc@latest')
}

& $IconGenerator -OutputPath $IconSource
if ($LASTEXITCODE -ne 0) {
    throw 'Failed to generate application icon.'
}

Invoke-NativeOrThrow -FilePath $RsrcExe -Arguments @('-manifest', $ManifestSource, '-ico', $IconSource, '-arch', 'amd64', '-o', $ResourceOutput)

if (Test-Path $LegacyResourceOutput) {
    Remove-Item -Path $LegacyResourceOutput -Force -ErrorAction SilentlyContinue
}

if ($Clean) {
    Remove-Item -Path $DistDir -Recurse -Force -ErrorAction SilentlyContinue
}

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

Push-Location $ProjectRoot
try {
    Invoke-NativeOrThrow -FilePath $script:GoExe -Arguments @('mod', 'download')
    Invoke-NativeOrThrow -FilePath $script:GoExe -Arguments @('build', '-trimpath', '-ldflags=-H windowsgui', '-o', $ExePath, './cmd/magnifier')
    Copy-Item -Path $ManifestSource -Destination $ManifestTarget -Force
    Copy-Item -Path $IconSource -Destination $IconTarget -Force
}
finally {
    Pop-Location
}

Write-Host ("Build completed: {0}" -f $ExePath)