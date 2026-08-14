# 从 icon.svg 的设计重绘多尺寸图标并打包成 icon.ico
# （GDI+ 绘制；16/24/32/48/64 用 DIB，128/256 用 PNG；与 main.go 内嵌的 SVG 同款设计）

Add-Type -AssemblyName System.Drawing

function New-RoundedPath([float]$x, [float]$y, [float]$w, [float]$h, [float]$r) {
    $p = New-Object System.Drawing.Drawing2D.GraphicsPath
    $d = $r * 2
    $p.AddArc($x, $y, $d, $d, 180, 90)
    $p.AddArc($x + $w - $d, $y, $d, $d, 270, 90)
    $p.AddArc($x + $w - $d, $y + $h - $d, $d, $d, 0, 90)
    $p.AddArc($x, $y + $h - $d, $d, $d, 90, 90)
    $p.CloseFigure()
    return $p
}

function Draw-Icon([int]$size) {
    $bmp = New-Object System.Drawing.Bitmap($size, $size, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.Clear([System.Drawing.Color]::Transparent)

    $f = $size / 256.0
    $C = { param($hex) [System.Drawing.ColorTranslator]::FromHtml($hex) }

    $bg = & $C '#2B2B33'; $border = & $C '#3F3F4A'
    $side = & $C '#3A3A45'; $row = & $C '#5C5C68'
    $active = & $C '#FFB347'; $gold = & $C '#FFD700'; $bubble = & $C '#4A4A55'

    # 背景圆角方块 + 描边
    $path = New-RoundedPath (8*$f) (8*$f) (240*$f) (240*$f) (56*$f)
    $g.FillPath((New-Object System.Drawing.SolidBrush($bg)), $path)
    $pen = New-Object System.Drawing.Pen($border, [Math]::Max(1.0, 4*$f))
    $g.DrawPath($pen, $path)

    # 左侧会话栏
    $path2 = New-RoundedPath (30*$f) (36*$f) (64*$f) (184*$f) (18*$f)
    $g.FillPath((New-Object System.Drawing.SolidBrush($side)), $path2)

    # 收藏星（与 icon.svg 相同坐标）
    $pts = @(
        [System.Drawing.PointF]::new(62*$f, 50*$f), [System.Drawing.PointF]::new(66.8*$f, 59.7*$f),
        [System.Drawing.PointF]::new(77.5*$f, 60.4*$f), [System.Drawing.PointF]::new(69.8*$f, 67.2*$f),
        [System.Drawing.PointF]::new(72.3*$f, 77.6*$f), [System.Drawing.PointF]::new(62*$f, 71.9*$f),
        [System.Drawing.PointF]::new(51.7*$f, 77.6*$f), [System.Drawing.PointF]::new(54.2*$f, 67.2*$f),
        [System.Drawing.PointF]::new(46.5*$f, 60.4*$f), [System.Drawing.PointF]::new(57.2*$f, 59.7*$f)
    )
    $g.FillPolygon((New-Object System.Drawing.SolidBrush($gold)), $pts)

    # 会话行
    foreach ($y in @(94, 113, 132, 151)) {
        $c = if ($y -eq 94) { $active } else { $row }
        $rp = New-RoundedPath (44*$f) ($y*$f) (36*$f) (11*$f) (5.5*$f)
        $g.FillPath((New-Object System.Drawing.SolidBrush($c)), $rp)
    }

    # 对话气泡
    $bp = New-RoundedPath (110*$f) (52*$f) (116*$f) (104*$f) (26*$f)
    $g.FillPath((New-Object System.Drawing.SolidBrush($bubble)), $bp)

    # 三个点
    foreach ($cx in @(144, 168, 192)) {
        $g.FillEllipse((New-Object System.Drawing.SolidBrush($active)), ($cx-8)*$f, (104-8)*$f, 16*$f, 16*$f)
    }

    $g.Dispose()
    return $bmp
}

function ConvertTo-Dib([System.Drawing.Bitmap]$bmp) {
    $w = $bmp.Width; $h = $bmp.Height
    $rect = New-Object System.Drawing.Rectangle(0, 0, $w, $h)
    $bd = $bmp.LockBits($rect, [System.Drawing.Imaging.ImageLockMode]::ReadOnly, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $buf = New-Object byte[] ($bd.Stride * $h)
    [System.Runtime.InteropServices.Marshal]::Copy($bd.Scan0, $buf, 0, $buf.Length)
    $bmp.UnlockBits($bd)

    $ms = New-Object System.IO.MemoryStream
    $bw = New-Object System.IO.BinaryWriter($ms)
    $bw.Write([uint32]40)                     # biSize
    $bw.Write([int32]$w)                      # biWidth
    $bw.Write([int32]($h * 2))                # biHeight = XOR + AND
    $bw.Write([uint16]1)                      # planes
    $bw.Write([uint16]32)                     # bitcount（BGRA）
    $bw.Write([uint32]0)                      # BI_RGB
    $bw.Write([uint32]($h * $w * 4))          # biSizeImage
    $bw.Write([int32]0); $bw.Write([int32]0); $bw.Write([uint32]0); $bw.Write([uint32]0)
    for ($y = $h - 1; $y -ge 0; $y--) {       # DIB 行序自下而上
        $bw.Write($buf, $y * $bd.Stride, $w * 4)
    }
    $maskRow = [int]([Math]::Ceiling($w / 32.0) * 4)
    $bw.Write((New-Object byte[] ($maskRow * $h)))  # AND 掩码全 0（由 alpha 驱动）
    $bw.Flush()
    $bytes = $ms.ToArray()
    $ms.Dispose(); $bw.Dispose()
    return $bytes
}

$entries = New-Object System.Collections.Generic.List[object]
foreach ($s in @(16, 24, 32, 48, 64, 128, 256)) {
    $bmp = Draw-Icon $s
    # 全部用 DIB：windres 的 ICO 解析器不支持 PNG 条目
    $entries.Add(@{ W = $s; H = $s; Data = (ConvertTo-Dib $bmp) })
    $bmp.Dispose()
}

$ms = New-Object System.IO.MemoryStream
$w = New-Object System.IO.BinaryWriter($ms)
$w.Write([uint16]0)                          # reserved
$w.Write([uint16]1)                          # type: icon
$w.Write([uint16]$entries.Count)
$offset = 6 + 16 * $entries.Count
foreach ($e in $entries) {
    $w.Write([byte]$(if ($e.W -ge 256) { 0 } else { $e.W }))
    $w.Write([byte]$(if ($e.H -ge 256) { 0 } else { $e.H }))
    $w.Write([byte]0)                         # color count
    $w.Write([byte]0)                         # reserved
    $w.Write([uint16]1)                       # planes
    $w.Write([uint16]32)                      # bit count
    $w.Write([uint32]$e.Data.Length)
    $w.Write([uint32]$offset)
    $offset += $e.Data.Length
}
foreach ($e in $entries) { $w.Write([byte[]]$e.Data) }  # 必须显式转 byte[]，否则 PowerShell 重载解析只写 1 字节
$w.Flush()
[System.IO.File]::WriteAllBytes("$PSScriptRoot\icon.ico", $ms.ToArray())
Write-Host "icon.ico written: $((Get-Item "$PSScriptRoot\icon.ico").Length) bytes, $($entries.Count) entries"
