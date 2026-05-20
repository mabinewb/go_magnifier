[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'

Add-Type -AssemblyName System.Drawing
if (-not ("GoMagnifierNativeIconMethods" -as [type])) {
    Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class GoMagnifierNativeIconMethods {
    [DllImport("user32.dll", SetLastError = true)]
    public static extern bool DestroyIcon(IntPtr hIcon);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern uint PrivateExtractIcons(
        string szFileName,
        int nIconIndex,
        int cxIcon,
        int cyIcon,
        IntPtr[] phicon,
        uint[] piconid,
        uint nIcons,
        uint flags);
}
"@
}

function New-Shell32IconFrameBytes {
    param(
        [Parameter(Mandatory = $true)]
        [int]$Size
    )

    $shell32Path = Join-Path $env:WINDIR 'System32\shell32.dll'
    $iconIndex = 22
    $handles = [IntPtr[]]::new(1)
    $ids = [uint32[]]::new(1)
    $extracted = [GoMagnifierNativeIconMethods]::PrivateExtractIcons($shell32Path, $iconIndex, $Size, $Size, $handles, $ids, 1, 0)
    if ($extracted -eq 0 -or $handles[0] -eq [IntPtr]::Zero) {
        throw "Failed to extract shell32 icon index $iconIndex at ${Size}x${Size}."
    }

    $bitmap = New-Object System.Drawing.Bitmap $Size, $Size
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $icon = $null
    $stream = $null

    try {
        $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
        $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.Clear([System.Drawing.Color]::Transparent)

        $icon = [System.Drawing.Icon]::FromHandle($handles[0])
        $graphics.DrawIcon($icon, [System.Drawing.Rectangle]::FromLTRB(0, 0, $Size, $Size))

        $stream = New-Object System.IO.MemoryStream
        $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
        $bytes = $stream.ToArray()
        return ,$bytes
    }
    finally {
        if ($stream -ne $null) {
            $stream.Dispose()
        }
        if ($icon -ne $null) { $icon.Dispose() }
        if ($graphics -ne $null) { $graphics.Dispose() }
        if ($bitmap -ne $null) { $bitmap.Dispose() }
        if ($handles[0] -ne [IntPtr]::Zero) {
            [GoMagnifierNativeIconMethods]::DestroyIcon($handles[0]) | Out-Null
        }
    }
}

function Write-MultiSizeIcon {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [object[]]$Frames
    )

    $fileStream = [System.IO.File]::Open($Path, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write)
    $writer = New-Object System.IO.BinaryWriter($fileStream)
    try {
        $writer.Write([UInt16]0)
        $writer.Write([UInt16]1)
        $writer.Write([UInt16]$Frames.Count)

        $offset = 6 + ($Frames.Count * 16)
        foreach ($frame in $Frames) {
            $dimension = if ($frame.Size -ge 256) { 0 } else { [byte]$frame.Size }
            $writer.Write([byte]$dimension)
            $writer.Write([byte]$dimension)
            $writer.Write([byte]0)
            $writer.Write([byte]0)
            $writer.Write([UInt16]1)
            $writer.Write([UInt16]32)
            $writer.Write([UInt32]$frame.Bytes.Length)
            $writer.Write([UInt32]$offset)
            $offset += $frame.Bytes.Length
        }

        foreach ($frame in $Frames) {
            $writer.Write($frame.Bytes)
        }
    }
    finally {
        $writer.Dispose()
        $fileStream.Dispose()
    }
}

$outputDir = Split-Path -Parent $OutputPath
if (-not (Test-Path $outputDir)) {
    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
}

$sizes = @(16, 24, 32, 48, 64, 128, 256)
$frames = foreach ($size in $sizes) {
    [PSCustomObject]@{
        Size = $size
        Bytes = [byte[]](New-Shell32IconFrameBytes -Size $size)
    }
}

Write-MultiSizeIcon -Path $OutputPath -Frames $frames
