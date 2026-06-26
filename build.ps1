# AudioStream 构建脚本
# 使用: .\build.ps1

param(
    [switch]$Clean,
    [string]$OutputDir = "."
)

# 设置颜色
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

# 启用 CGO（WASAPI 捕获需要）
$env:CGO_ENABLED = "1"

# 清理
if ($Clean -and (Test-Path "$OutputDir\server.exe")) {
    Remove-Item "$OutputDir\server.exe" -ErrorAction SilentlyContinue
    Write-Host "已清理旧的 server.exe" -ForegroundColor $Yellow
}

Write-Host ""
Write-Host "📦 正在构建 server.exe..." -ForegroundColor $Yellow
go build -ldflags="-s -w" -o "$OutputDir\server.exe" -v ./cmd/server 2>&1
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
Write-Host ""
Write-Host "使用方法:" -ForegroundColor $Yellow
Write-Host "  发送端 (音频源电脑): server.exe" -ForegroundColor $Yellow
