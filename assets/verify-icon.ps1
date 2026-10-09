# Verify the TheOne icon in the Windows PE resources and the shell icon.
param(
    [string]$Path = (Join-Path (Split-Path $PSScriptRoot -Parent) 'claude-terminal.exe'),
    [string]$PreviewPath = ''
)
$ErrorActionPreference = 'Stop'
$src = @'
using System;
using System.IO;
using System.ComponentModel;
using System.Runtime.InteropServices;
public static class TheOneIconRes {
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern IntPtr LoadLibraryEx(string file, IntPtr hFile, uint flags);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr FindResource(IntPtr module, IntPtr name, IntPtr type);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern uint SizeofResource(IntPtr module, IntPtr resource);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr LoadResource(IntPtr module, IntPtr resource);
    [DllImport("kernel32.dll")]
    static extern IntPtr LockResource(IntPtr resource);
    [DllImport("kernel32.dll")]
    static extern bool FreeLibrary(IntPtr module);
    static byte[] Read(IntPtr module, int id, int type) {
        IntPtr info = FindResource(module, new IntPtr(id), new IntPtr(type));
        if (info == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
        int size = checked((int)SizeofResource(module, info));
        IntPtr data = LockResource(LoadResource(module, info));
        if (size == 0 || data == IntPtr.Zero) throw new InvalidDataException("Empty icon resource");
        byte[] bytes = new byte[size];
        Marshal.Copy(data, bytes, 0, size);
        return bytes;
    }
    public static int Verify(string file, string icoFile) {
        byte[] ico = File.ReadAllBytes(icoFile);
        IntPtr module = LoadLibraryEx(file, IntPtr.Zero, 2);
        if (module == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
        try {
            byte[] group = Read(module, 3, 14);
            int count = BitConverter.ToUInt16(ico, 4);
            if (count == 0 || BitConverter.ToUInt16(group, 4) != count)
                throw new InvalidDataException("Icon frame count mismatch");
            for (int i = 0; i < count; i++) {
                int source = 6 + i * 16, target = 6 + i * 14;
                for (int j = 0; j < 12; j++)
                    if (ico[source + j] != group[target + j])
                        throw new InvalidDataException("Icon frame metadata mismatch");
                int id = BitConverter.ToUInt16(group, target + 12);
                byte[] frame = Read(module, id, 3);
                int length = checked((int)BitConverter.ToUInt32(ico, source + 8));
                int offset = checked((int)BitConverter.ToUInt32(ico, source + 12));
                if (frame.Length != length) throw new InvalidDataException("Icon frame size mismatch");
                for (int j = 0; j < length; j++)
                    if (frame[j] != ico[offset + j])
                        throw new InvalidDataException("Icon frame pixels mismatch");
            }
            return count;
        } finally { FreeLibrary(module); }
    }
}
'@
if (-not ('TheOneIconRes' -as [type])) { Add-Type -TypeDefinition $src }
$p = (Resolve-Path -LiteralPath $Path).Path
$count = [TheOneIconRes]::Verify($p, (Join-Path $PSScriptRoot 'icon.ico'))
Write-Output "RT_GROUP_ICON 3: all $count frames match assets/icon.ico"
Add-Type -AssemblyName System.Drawing
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($p)
if ($null -eq $icon) { throw 'Windows could not extract the file icon' }
$bmp = $null
try {
    $bmp = $icon.ToBitmap()
    $indigo = 0
    $ivory = 0
    for ($x = 0; $x -lt $bmp.Width; $x++) {
        for ($y = 0; $y -lt $bmp.Height; $y++) {
            $c = $bmp.GetPixel($x, $y)
            if ($c.A -lt 128) { continue }
            if ($c.B -gt 150 -and $c.R -lt 130 -and $c.G -lt 130) { $indigo++ }
            if ($c.R -gt 230 -and $c.G -gt 230 -and $c.B -gt 230) { $ivory++ }
        }
    }
    if ($indigo -eq 0 -or $ivory -eq 0) { throw 'Unexpected shell icon colors' }
    Write-Output ("Shell icon {0}x{1}: indigo={2}, ivory={3}" -f $bmp.Width, $bmp.Height, $indigo, $ivory)
    if ($PreviewPath) { $bmp.Save([IO.Path]::GetFullPath($PreviewPath), [System.Drawing.Imaging.ImageFormat]::Png) }
} finally {
    if ($null -ne $bmp) { $bmp.Dispose() }
    $icon.Dispose()
}
