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

function Get-GitTextOrDefault {
    param(
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$DefaultValue
    )

    $output = & git @Arguments 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $DefaultValue
    }

    $text = ($output | Out-String).Trim()
    if ([string]::IsNullOrWhiteSpace($text)) {
        return $DefaultValue
    }

    return $text
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
    $BuildVersion = Get-GitTextOrDefault -Arguments @('describe', '--tags', '--dirty', '--always') -DefaultValue 'dev'
    $BuildCommit = Get-GitTextOrDefault -Arguments @('rev-parse', '--short', 'HEAD') -DefaultValue 'unknown'
    $BuildTimeUtc = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $BuildVersionSafe = $BuildVersion.Replace('/', '-').Replace(':', '-')
    $PackageDir = Join-Path $DistDir ("GoMagnifier-{0}-windows-amd64" -f $BuildVersionSafe)
    $PackageZipPath = Join-Path $DistDir ("GoMagnifier-{0}-windows-amd64.zip" -f $BuildVersionSafe)
    $LdFlags = @(
        '-H windowsgui',
        "-X gomagnifier/internal/version.Version=$BuildVersion",
        "-X gomagnifier/internal/version.Commit=$BuildCommit",
        "-X gomagnifier/internal/version.BuildTime=$BuildTimeUtc"
    ) -join ' '
    Write-Host ("Build version: {0} ({1})" -f $BuildVersion, $BuildCommit)
    Write-Host ("Build time (UTC): {0}" -f $BuildTimeUtc)
    Invoke-NativeOrThrow -FilePath $script:GoExe -Arguments @('mod', 'download')
    Invoke-NativeOrThrow -FilePath $script:GoExe -Arguments @('build', '-trimpath', "-ldflags=$LdFlags", '-o', $ExePath, './cmd/magnifier')
    Copy-Item -Path $ManifestSource -Destination $ManifestTarget -Force
    Copy-Item -Path $IconSource -Destination $IconTarget -Force
    Remove-Item -Path $PackageDir, $PackageZipPath -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $PackageDir | Out-Null
    Copy-Item -Path $ExePath -Destination $PackageDir -Force
    Copy-Item -Path $ManifestTarget -Destination $PackageDir -Force
    Copy-Item -Path $IconTarget -Destination $PackageDir -Force
    Compress-Archive -Path (Join-Path $PackageDir '*') -DestinationPath $PackageZipPath -Force
}
finally {
    Pop-Location
}

Write-Host ("Build completed: {0}" -f $ExePath)
Write-Host ("Release package: {0}" -f $PackageZipPath)