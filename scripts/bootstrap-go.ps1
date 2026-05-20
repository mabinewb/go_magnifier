[CmdletBinding()]
param(
    [switch]$ForceDownload
)

$ErrorActionPreference = 'Stop'

$ProjectRoot = Split-Path $PSScriptRoot -Parent
$AppDataRoot = Join-Path $env:LOCALAPPDATA 'GoMagnifier'
$ToolsDir = Join-Path $AppDataRoot 'toolchain'
$DownloadsDir = Join-Path $ToolsDir 'downloads'
$LocalGoRoot = Join-Path $ToolsDir 'go'
$LocalGoExe = Join-Path $LocalGoRoot 'bin\go.exe'

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

function Use-GoBinary {
    param([Parameter(Mandatory = $true)][string]$GoExe)

    $script:GoExe = $GoExe
    $env:GOTOOLCHAIN = 'local'
    $env:Path = "$(Split-Path $GoExe);$env:Path"
}

if (-not $ForceDownload) {
    $systemGo = Get-Command go -ErrorAction SilentlyContinue
    if ($systemGo) {
        Use-GoBinary -GoExe $systemGo.Source
    }
}

if (-not $script:GoExe) {
    if ($ForceDownload -or -not (Test-Path $LocalGoExe)) {
        New-Item -ItemType Directory -Force -Path $ToolsDir, $DownloadsDir | Out-Null

        $release = Invoke-RestMethod -Uri 'https://go.dev/dl/?mode=json'
        $archive = $release |
            Where-Object { $_.stable } |
            Select-Object -First 1 |
            ForEach-Object {
                $_.files |
                    Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'archive' } |
                    Select-Object -First 1
            }

        if (-not $archive) {
            throw 'Windows amd64용 Go 아카이브를 찾지 못했습니다.'
        }

        $zipPath = Join-Path $DownloadsDir $archive.filename
        $extractPath = Join-Path $DownloadsDir 'go-extract'

        Invoke-WebRequest -Uri ("https://go.dev/dl/{0}" -f $archive.filename) -OutFile $zipPath
        Remove-Item -Path $extractPath, $LocalGoRoot -Recurse -Force -ErrorAction SilentlyContinue
        Expand-Archive -Path $zipPath -DestinationPath $extractPath -Force
        Move-Item -Path (Join-Path $extractPath 'go') -Destination $LocalGoRoot
    }

    if (-not (Test-Path $LocalGoExe)) {
        throw '로컬 Go 설치에 실패했습니다.'
    }

    Use-GoBinary -GoExe $LocalGoExe
}

Write-Host ("Using Go: {0}" -f $script:GoExe)
Invoke-NativeOrThrow -FilePath $script:GoExe -Arguments @('version')