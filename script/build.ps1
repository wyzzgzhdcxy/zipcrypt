# ============================================================
#  zipcrypt build script (PowerShell)
#    `wails3 build` runs the whole chain: generate TS bindings
#    -> build frontend -> generate icon/syso -> go build
#    Output: bin\zipcrypt.exe, then copied to D:\app_mgr\my\
#
#  Usage:
#    .\script\build.ps1
# ============================================================

$ErrorActionPreference = 'Stop'

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BinDir      = Join-Path $ProjectRoot 'bin'
$ExeName     = 'zipcrypt.exe'
$ExePath     = Join-Path $BinDir $ExeName
$DestDir     = 'D:\app_mgr\my'
$DestPath    = Join-Path $DestDir $ExeName

Set-Location -LiteralPath $ProjectRoot

if (-not (Get-Command wails3 -ErrorAction SilentlyContinue)) {
    Write-Host '[ERROR] wails3 CLI not found. Install it with:' -ForegroundColor Red
    Write-Host '   go install github.com/wailsapp/wails/v3/cmd/wails3@latest'
    exit 1
}

Write-Host '[1/3] go vet + go test ...'
go vet ./...
if ($LASTEXITCODE -ne 0) { Write-Host '[ERROR] go vet failed' -ForegroundColor Red; exit 1 }
go test ./...
if ($LASTEXITCODE -ne 0) { Write-Host '[ERROR] go test failed' -ForegroundColor Red; exit 1 }

Write-Host '[2/3] wails3 build ...'
wails3 build
if ($LASTEXITCODE -ne 0) {
    Write-Host ''
    Write-Host '[ERROR] build failed (exit code ' $LASTEXITCODE ')' -ForegroundColor Red
    exit $LASTEXITCODE
}

if (-not (Test-Path -LiteralPath $ExePath)) {
    Write-Host "[ERROR] expected output not found: $ExePath" -ForegroundColor Red
    exit 1
}

# 目标 exe 正在运行时，Copy-Item 会直接报"文件正在使用"，复制前先结束进程
function Stop-RunningApp {
    $Running = Get-Process -Name 'zipcrypt' -ErrorAction SilentlyContinue
    if (-not $Running) { return $false }
    foreach ($P in $Running) {
        Write-Host '[..] stopping running zipcrypt (PID ' $P.Id ')'
        try {
            Stop-Process -Id $P.Id -Force -ErrorAction Stop
        } catch {
            Write-Host '[WARN] cannot stop PID ' $P.Id ': ' $_.Exception.Message -ForegroundColor Yellow
        }
    }
    Start-Sleep -Milliseconds 600
    return $true
}

Write-Host '[3/3] deploy ...'
Stop-RunningApp | Out-Null

if (-not (Test-Path -LiteralPath $DestDir)) {
    New-Item -ItemType Directory -Path $DestDir -Force | Out-Null
}

$Deployed = $false
for ($Attempt = 1; $Attempt -le 3; $Attempt++) {
    try {
        Copy-Item -LiteralPath $ExePath -Destination $DestPath -Force -ErrorAction Stop
        $Deployed = $true
        break
    } catch {
        if ($Attempt -eq 3) { break }
        Write-Host '[..] copy failed, retry ' $Attempt '/3: ' $_.Exception.Message -ForegroundColor Yellow
        Start-Sleep -Milliseconds 800
        Stop-RunningApp | Out-Null
    }
}

$Size = '{0:N1} MB' -f ((Get-Item -LiteralPath $ExePath).Length / 1MB)
Write-Host ''
Write-Host '[OK] build finished: ' $ExePath + '  (' $Size ')'
if ($Deployed) {
    Write-Host '[OK] deployed to:   ' $DestPath
} else {
    Write-Host '[WARN] deploy failed, exe still available at: ' $ExePath -ForegroundColor Yellow
}
