# verify-icon.ps1 验证 exe 内 RT_GROUP_ICON ID 3 资源是否存在且为设计图标
param([string]$Path = (Join-Path (Split-Path $PSScriptRoot -Parent) 'claude-terminal.exe'))

$src = @'
using System;
using System.Runtime.InteropServices;
public static class IconRes {
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern IntPtr LoadLibraryEx(string lpFileName, IntPtr hFile, uint dwFlags);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern IntPtr FindResource(IntPtr hModule, IntPtr lpName, IntPtr lpType);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern uint SizeofResource(IntPtr hModule, IntPtr hResInfo);
    [DllImport("kernel32.dll")]
    public static extern bool FreeLibrary(IntPtr hModule);
    public static IntPtr MAKEINTRESOURCE(ushort v) { return new IntPtr(v); }
    public static uint HasGroupIcon(string file, ushort id) {
        IntPtr h = LoadLibraryEx(file, IntPtr.Zero, 0x00000002); // LOAD_LIBRARY_AS_DATAFILE
        if (h == IntPtr.Zero) return 0;
        IntPtr r = FindResource(h, MAKEINTRESOURCE(id), MAKEINTRESOURCE(14)); // RT_GROUP_ICON=14
        uint sz = (r == IntPtr.Zero) ? 0 : SizeofResource(h, r);
        FreeLibrary(h);
        return sz;
    }
}
'@
Add-Type -TypeDefinition $src

$p = (Resolve-Path $Path).Path
foreach ($id in @(1, 3)) {
    $sz = [IconRes]::HasGroupIcon($p, $id)
    Write-Output ("ID {0}: {1} bytes (0 = 不存在)" -f $id, $sz)
}

# 文件图标（资源管理器视角，ExtractAssociatedIcon 取首个图标组）颜色统计
Add-Type -AssemblyName System.Drawing
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($p)
$bmp = $icon.ToBitmap()
$orange = 0; $dark = 0; $total = 0
for ($x = 0; $x -lt $bmp.Width; $x++) {
    for ($y = 0; $y -lt $bmp.Height; $y++) {
        $c = $bmp.GetPixel($x, $y)
        if ($c.A -lt 128) { continue }
        $total++
        if ($c.R -gt 180 -and $c.G -gt 100 -and $c.G -lt 220 -and $c.B -lt 120) { $orange++ }
        elseif ($c.R -lt 90 -and $c.G -lt 90 -and $c.B -lt 110) { $dark++ }
    }
}
Write-Output ("文件图标(32x32): opaque={0} dark={1} orange={2}" -f $total, $dark, $orange)
$icon.Dispose(); $bmp.Dispose()