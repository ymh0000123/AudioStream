# AudioStream 构建脚本
# 使用: .\build.ps1

param(
    [switch]$Clean,
    [string]$OutputDir = "."
)

$Green = "Green"
$Red = "Red"
$Yellow = "Yellow"
$Cyan = "Cyan"

Write-Host "========================================" -ForegroundColor $Cyan
Write-Host "  AudioStream - 跨平台音频传输工具构建" -ForegroundColor $Cyan
Write-Host "========================================" -ForegroundColor $Cyan
Write-Host ""

# 检查 Go 环境
$goVersion = go version 2>$null
if (-not $?) {
    Write-Host "❌ 未找到 Go 环境，请先安装 Go 1.21+" -ForegroundColor $Red
    exit 1
}
Write-Host "✅ Go: $goVersion" -ForegroundColor $Green

$env:CGO_ENABLED = "1"

# 清理
if ($Clean) {
    @("$OutputDir\server.exe", "$OutputDir\smtc.dll", "internal\webplayer\smtc_embed.dll") | ForEach-Object {
        if (Test-Path $_) { Remove-Item $_ -ErrorAction SilentlyContinue; Write-Host "已清理 $_" -ForegroundColor $Yellow }
    }
}

# 构建 smtc.dll (必须先于 Go 构建，因为 go:embed)
Write-Host ""
Write-Host "📦 正在构建 smtc.dll..." -ForegroundColor $Yellow

# 查找 VS Build Tools 并设置环境
$vsWhere = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe"
$vcvars = $null
if (Test-Path $vsWhere) {
    $vsPath = & $vsWhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath 2>$null
    if ($vsPath) {
        $vcvars = Join-Path $vsPath "VC\Auxiliary\Build\vcvarsall.bat"
    }
}
if (-not $vcvars -or -not (Test-Path $vcvars)) {
    # 尝试默认路径
    $candidates = @(
        "${env:ProgramFiles(x86)}\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvarsall.bat",
        "${env:ProgramFiles(x86)}\Microsoft Visual Studio\2022\Community\VC\Auxiliary\Build\vcvarsall.bat",
        "${env:ProgramFiles(x86)}\Microsoft Visual Studio\2022\Professional\VC\Auxiliary\Build\vcvarsall.bat",
        "${env:ProgramFiles(x86)}\Microsoft Visual Studio\2022\Enterprise\VC\Auxiliary\Build\vcvarsall.bat"
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { $vcvars = $c; break }
    }
}

$nmakeOk = $false
if ($vcvars) {
    Push-Location dll
    cmd /c "call `"$vcvars`" amd64 >nul 2>&1 && nmake /f Makefile" 2>&1
    $nmakeOk = $?
    Pop-Location
} else {
    Write-Host "⚠️  未找到 Visual Studio Build Tools，跳过 DLL 构建" -ForegroundColor $Yellow
}
if (-not $nmakeOk -or -not (Test-Path "dll\smtc.dll")) {
    Write-Host "❌ smtc.dll 构建失败" -ForegroundColor $Red
    Write-Host "   SMTC 状态查询将降级为不可用" -ForegroundColor $Yellow
} else {
    # 复制到 webplayer 目录供 go:embed 嵌入
    Copy-Item "dll\smtc.dll" "internal\webplayer\smtc_embed.dll" -Force
    Write-Host "✅ smtc.dll 构建成功" -ForegroundColor $Green
}

# 构建 server.exe
Write-Host ""
Write-Host "📦 正在构建 server.exe..." -ForegroundColor $Yellow

# 获取版本信息用于注入
$gitTag = ""
$commitHash = ""
try { $gitTag = git describe --tags --abbrev=0 2>$null } catch {}
if (-not $gitTag) { $gitTag = "v0.0.0-dev" }
try { $commitHash = git rev-parse --short HEAD 2>$null } catch {}
if (-not $commitHash) { $commitHash = "none" }
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X main.version=$gitTag -X main.commit=$commitHash -X main.buildDate=$buildTime"

go build -ldflags="$ldflags" -o "$OutputDir\server.exe" ./cmd/server 2>&1
if (-not $?) {
    Write-Host "❌ server.exe 构建失败" -ForegroundColor $Red
    exit 1
}
Write-Host "✅ server.exe 构建成功" -ForegroundColor $Green

# 显示文件信息
Write-Host ""
Write-Host "📊 构建结果:" -ForegroundColor $Cyan
$serverSize = (Get-Item "$OutputDir\server.exe").Length
Write-Host "  server.exe: $([math]::Round($serverSize/1KB)) KB" -ForegroundColor $Cyan

Write-Host ""
Write-Host "✅ 构建完成！" -ForegroundColor $Green
