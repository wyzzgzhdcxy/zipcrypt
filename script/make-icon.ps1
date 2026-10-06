# ============================================================
#  生成 Windows 应用图标
#    输入：一张 2048x2048 的正方形图（自动裁掉四周白边）
#    输出：build\appicon.png (1024x1024) + build\windows\icon.ico
#
#  用法：
#    .\script\make-icon.ps1 <图片路径>
#
#  例：
#    .\script\make-icon.ps1 C:\Users\wangchaojun\AppData\Local\Temp\zipcrypt-icon\tile.jpg
# ============================================================

param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Source,

    [int]$Size = 1024
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$OutPng = Join-Path $ProjectRoot 'build\appicon.png'

if (-not (Test-Path -LiteralPath $Source)) {
    Write-Host "[ERROR] 找不到源图: $Source" -ForegroundColor Red
    exit 1
}

$src = [System.Drawing.Image]::FromFile((Resolve-Path -LiteralPath $Source))
try {
    # ---- 1. 扫描非白像素范围，把四周留白裁掉 ----
    $bmp = New-Object System.Drawing.Bitmap $src
    $minX = $bmp.Width; $minY = $bmp.Height; $maxX = -1; $maxY = -1
    for ($y = 0; $y -lt $bmp.Height; $y += 2) {
        for ($x = 0; $x -lt $bmp.Width; $x += 2) {
            $c = $bmp.GetPixel($x, $y)
            # 接近纯白就算背景（阈值 244，容忍 JPEG 压缩噪点）
            if ($c.R -lt 244 -or $c.G -lt 244 -or $c.B -lt 244) {
                if ($x -lt $minX) { $minX = $x }
                if ($x -gt $maxX) { $maxX = $x }
                if ($y -lt $minY) { $minY = $y }
                if ($y -gt $maxY) { $maxY = $y }
            }
        }
    }
    $bmp.Dispose()

    if ($maxX -le $minX -or $maxY -le $minY) {
        Write-Host '[ERROR] 没找到非白内容，源图可能是纯白' -ForegroundColor Red
        exit 1
    }
    Write-Host ("[1/3] 内容范围: {0},{1} - {2},{3}" -f $minX, $minY, $maxX, $maxY)

    # ---- 2. 裁剪 + 缩放到目标尺寸 ----
    $cropW = $maxX - $minX + 1
    $cropH = $maxY - $minY + 1
    $rect = New-Object System.Drawing.Rectangle $minX, $minY, $cropW, $cropH

    $dst = New-Object System.Drawing.Bitmap $Size, $Size
    $dst.SetResolution(96, 96)
    $g = [System.Drawing.Graphics]::FromImage($dst)
    $g.InterpolationMode = 'HighQualityBicubic'
    $g.SmoothingMode = 'HighQuality'
    $g.PixelOffsetMode = 'HighQuality'
    $g.CompositingQuality = 'HighQuality'
    # 透明底，避免小尺寸时出现白边
    $g.Clear([System.Drawing.Color]::Transparent)
    $g.DrawImage($src, (New-Object System.Drawing.Rectangle 0, 0, $Size, $Size),
                 $rect, [System.Drawing.GraphicsUnit]::Pixel)
    $g.Dispose()

    $dst.Save($OutPng, [System.Drawing.Imaging.ImageFormat]::Png)
    $dst.Dispose()
}
finally {
    $src.Dispose()
}

Write-Host "[2/3] 已生成 $OutPng ($Size x $Size)"

# ---- 3. 交给 wails3 生成 .ico / .icns ----
# 注意：这些开关里的路径是相对 build 目录的（Taskfile 里 generate:icons 也是这么调的），
# 所以必须切到 build 目录再执行
Set-Location -LiteralPath (Join-Path $ProjectRoot 'build')
wails3 generate icons -input 'appicon.png' -windowsfilename 'windows/icon.ico' `
    -macfilename 'darwin/icons.icns' -iconcomposerinput 'appicon.icon' -macassetdir 'darwin'
if ($LASTEXITCODE -ne 0) {
    Write-Host '[ERROR] wails3 generate icons 失败' -ForegroundColor Red
    exit 1
}
Set-Location -LiteralPath $ProjectRoot

$ico = Join-Path $ProjectRoot 'build\windows\icon.ico'
$fi = Get-Item -LiteralPath $ico
Write-Host ('[3/3] 已生成 {0} ({1:N0} 字节, 更新于 {2})' -f $ico, $fi.Length, $fi.LastWriteTime)
