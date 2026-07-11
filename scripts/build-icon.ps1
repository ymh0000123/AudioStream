param(
    [string]$VcVars = ""
)

$ErrorActionPreference = "Stop"
$iconDir = Join-Path $PSScriptRoot "..\cmd\server"
$svgPath = Join-Path $PSScriptRoot "..\internal\webplayer\icon.svg"
$icoPath = Join-Path $iconDir "audiostream.ico"
$sysoPath = Join-Path $iconDir "rsrc_windows_amd64.syso"

if (-not (Test-Path $svgPath)) {
    throw "未找到图标源文件: $svgPath"
}
Write-Host "✅ 图标源文件: $svgPath" -ForegroundColor Green

Add-Type -AssemblyName System.Drawing

# SVG 设计来源于 internal/webplayer/icon.svg
# viewBox="0 0 128 128", 渲染为 256x256 (2x scale)
$size = 256
$scale = 2

$bitmap = [System.Drawing.Bitmap]::new($size, $size)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
$graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality

# === 颜色 ===
$tealDark   = [System.Drawing.ColorTranslator]::FromHtml("#16b9a8")   # 整体背景（SVG: <rect width="128" height="128" rx="28" fill="#16b9a8">）
$tealInner  = [System.Drawing.ColorTranslator]::FromHtml("#0f8f86")   # 内框填充
$greenLight = [System.Drawing.ColorTranslator]::FromHtml("#d8fff7")   # 边框/声波
$white      = [System.Drawing.Color]::White                            # 扬声器
$yellow     = [System.Drawing.ColorTranslator]::FromHtml("#f7b955")   # 圆点

# === 1. 背景圆角矩形 <rect width="128" height="128" rx="28" fill="#16b9a8"> ===
# 用路径绘制圆角矩形，四个角保持透明（alpha=0），匹配 SVG 外观
$outerR = 28 * $scale
$outerPath = [System.Drawing.Drawing2D.GraphicsPath]::new()
$outerPath.AddArc(0, 0, $outerR * 2, $outerR * 2, 180, 90)
$outerPath.AddArc($size - $outerR * 2, 0, $outerR * 2, $outerR * 2, 270, 90)
$outerPath.AddArc($size - $outerR * 2, $size - $outerR * 2, $outerR * 2, $outerR * 2, 0, 90)
$outerPath.AddArc(0, $size - $outerR * 2, $outerR * 2, $outerR * 2, 90, 90)
$outerPath.CloseFigure()
$graphics.FillPath([System.Drawing.SolidBrush]::new($tealDark), $outerPath)
$outerPath.Dispose()

# === 2. 内框圆角矩形 <rect x="13" y="13" width="102" height="102" rx="21" fill="#0f8f86" stroke="#d8fff7" stroke-width="3"> ===
$innerX = 13 * $scale; $innerY = 13 * $scale
$innerW = 102 * $scale; $innerH = 102 * $scale
$innerR = 21 * $scale
$innerPath = [System.Drawing.Drawing2D.GraphicsPath]::new()
$innerPath.AddArc($innerX, $innerY, $innerR * 2, $innerR * 2, 180, 90)
$innerPath.AddArc($innerX + $innerW - $innerR * 2, $innerY, $innerR * 2, $innerR * 2, 270, 90)
$innerPath.AddArc($innerX + $innerW - $innerR * 2, $innerY + $innerH - $innerR * 2, $innerR * 2, $innerR * 2, 0, 90)
$innerPath.AddArc($innerX, $innerY + $innerH - $innerR * 2, $innerR * 2, $innerR * 2, 90, 90)
$innerPath.CloseFigure()
$graphics.FillPath([System.Drawing.SolidBrush]::new($tealInner), $innerPath)
$graphics.DrawPath([System.Drawing.Pen]::new($greenLight, 3 * $scale), $innerPath)

# === 3. 扬声器 <path d="M31 58h16l20-17v46L47 70H31z" fill="#ffffff"> ===
# 路径分解（SVG viewBox 坐标）:
#   M(31,58) → H(47,58) → L(67,41) → V(67,87) → L(47,70) → H(31,70) → Z
$speakerPoints = @(
    [System.Drawing.PointF]::new(31 * $scale, 58 * $scale),
    [System.Drawing.PointF]::new(47 * $scale, 58 * $scale),
    [System.Drawing.PointF]::new(67 * $scale, 41 * $scale),
    [System.Drawing.PointF]::new(67 * $scale, 87 * $scale),
    [System.Drawing.PointF]::new(47 * $scale, 70 * $scale),
    [System.Drawing.PointF]::new(31 * $scale, 70 * $scale)
)
$graphics.FillPolygon([System.Drawing.SolidBrush]::new($white), $speakerPoints)

# === 4. 声波弧线 <path d="M77 52c6 7 6 17 0 24M89 43c12 12 12 30 0 42" fill="none" stroke="#d8fff7" stroke-linecap="round" stroke-width="7"> ===
# 弧线 1: 从 (77,52) 到 (77,76), CP1=(83,59), CP2=(83,69)
# 弧线 2: 从 (89,43) 到 (89,85), CP1=(101,55), CP2=(101,73)
$wavePen = [System.Drawing.Pen]::new($greenLight, 7 * $scale)
$wavePen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
$wavePen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
$wavePen.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round

$wavePath = [System.Drawing.Drawing2D.GraphicsPath]::new()
# 弧线 1
$wavePath.AddBezier(
    [System.Drawing.PointF]::new(77 * $scale, 52 * $scale),
    [System.Drawing.PointF]::new(83 * $scale, 59 * $scale),
    [System.Drawing.PointF]::new(83 * $scale, 69 * $scale),
    [System.Drawing.PointF]::new(77 * $scale, 76 * $scale)
)
# 弧线 2
$wavePath.StartFigure()
$wavePath.AddBezier(
    [System.Drawing.PointF]::new(89 * $scale, 43 * $scale),
    [System.Drawing.PointF]::new(101 * $scale, 55 * $scale),
    [System.Drawing.PointF]::new(101 * $scale, 73 * $scale),
    [System.Drawing.PointF]::new(89 * $scale, 85 * $scale)
)
$graphics.DrawPath($wavePen, $wavePath)
$wavePath.Dispose()
$wavePen.Dispose()
$innerPath.Dispose()

# === 5. 黄色圆点 <circle cx="98" cy="33" r="5" fill="#f7b955"> ===
$dotR = 5 * $scale
$graphics.FillEllipse(
    [System.Drawing.SolidBrush]::new($yellow),
    98 * $scale - $dotR, 33 * $scale - $dotR,
    $dotR * 2, $dotR * 2
)

# === 生成 ICO（手动写入 32bpp 格式，避免 Icon.Save() 降级颜色）===
$bmpData = $bitmap.LockBits(
    [System.Drawing.Rectangle]::new(0, 0, $size, $size),
    [System.Drawing.Imaging.ImageLockMode]::ReadOnly,
    [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)

try {
    $stride = $bmpData.Stride
    $scan0 = $bmpData.Scan0
    $pixelBytes = [byte[]]::new($stride * $size)
    [System.Runtime.InteropServices.Marshal]::Copy($scan0, $pixelBytes, 0, $pixelBytes.Length)

    $icoStream = [System.IO.File]::Open($icoPath, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write)
    try {
        $writer = [System.IO.BinaryWriter]::new($icoStream)

        # ICO 文件头 (6 bytes)
        $writer.Write([uint16]0)           # reserved
        $writer.Write([uint16]1)           # type = ICO
        $writer.Write([uint16]1)           # count = 1

        # 目录项 (16 bytes) - 指向 32bpp BMP
        $writer.Write([byte]0)             # width (0=256)
        $writer.Write([byte]0)             # height (0=256)
        $writer.Write([byte]0)             # colors
        $writer.Write([byte]0)             # reserved
        $writer.Write([uint16]1)           # planes
        $writer.Write([uint16]32)          # bits per pixel

        # BMP 数据
        $bmpHeaderSize = 40
        $bmpPixelSize = $size * $size * 4
        $andMaskSize = (($size + 31) -band -32) / 8 * $size  # AND mask row is padded to 4-byte boundary
        $totalBmpSize = $bmpHeaderSize + $bmpPixelSize + $andMaskSize
        $offset = 6 + 16  # header + directory entry
        $writer.Write([uint32]$totalBmpSize)   # image data size
        $writer.Write([uint32]$offset)         # offset

        # BITMAPINFOHEADER (ICO 中 height = 实际高度 * 2，包含 XOR 和 AND mask)
        $writer.Write([uint32]40)              # biSize
        $writer.Write([int32]$size)            # biWidth
        $writer.Write([int32]($size * 2))      # biHeight (2x for XOR + AND mask)
        $writer.Write([uint16]1)               # biPlanes
        $writer.Write([uint16]32)              # biBitCount
        $writer.Write([uint32]0)               # biCompression (BI_RGB)
        $writer.Write([uint32]0)               # biSizeImage
        $writer.Write([int32]0)                # biXPelsPerMeter
        $writer.Write([int32]0)                # biYPelsPerMeter
        $writer.Write([uint32]0)               # biClrUsed
        $writer.Write([uint32]0)               # biClrImportant

        # XOR mask: 32bpp BGRA 像素，从底部行开始写入（ICO 使用 bottom-up 格式）
        for ($y = $size - 1; $y -ge 0; $y--) {
            $rowStart = $y * $stride
            for ($x = 0; $x -lt $size; $x++) {
                $p = $rowStart + $x * 4
                # Format32bppArgb 内存顺序 = B(b+0), G(b+1), R(b+2), A(b+3)
                # ICO 文件也要求 BGRA 顺序
                $writer.Write($pixelBytes[$p + 0])  # B
                $writer.Write($pixelBytes[$p + 1])  # G
                $writer.Write($pixelBytes[$p + 2])  # R
                $writer.Write($pixelBytes[$p + 3])  # A
            }
        }

        # AND mask: 1-bit 透明掩码（32bpp 图标用 alpha 通道，AND mask 全 0）
        $andRowBytes = (($size + 31) -band -32) / 8
        $andRow = [byte[]]::new($andRowBytes)
        for ($y = 0; $y -lt $size; $y++) {
            $writer.Write($andRow)
        }

        $writer.Flush()
    } finally {
        $icoStream.Dispose()
    }
} finally {
    $bitmap.UnlockBits($bmpData)
    $graphics.Dispose()
    $bitmap.Dispose()
}

# === 使用 rsrc 生成 .syso（纯 Go 实现，无需 Visual Studio Build Tools）===
$rsrc = Get-Command "rsrc" -ErrorAction SilentlyContinue
if (-not $rsrc) {
    Write-Host "⚠️  未找到 rsrc 工具，正在安装..." -ForegroundColor Yellow
    go install github.com/akavel/rsrc@latest 2>&1
    $rsrc = Get-Command "rsrc" -ErrorAction SilentlyContinue
    if (-not $rsrc) {
        throw "rsrc 安装失败"
    }
}

# 移除旧的 syso 文件
Remove-Item $sysoPath -ErrorAction SilentlyContinue

# 运行 rsrc
& $rsrc.Source -ico $icoPath -o $sysoPath 2>&1
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $sysoPath)) {
    throw "rsrc 图标资源编译失败"
}

Write-Host "✅ 应用图标已生成: $sysoPath" -ForegroundColor Green
Write-Host "   图标设计来源: internal\webplayer\icon.svg" -ForegroundColor Green
