# Aether Cockpit 卸载脚本（用户级）
#
#   powershell -ExecutionPolicy Bypass -File uninstall.ps1            # 删除程序与数据
#   powershell -ExecutionPolicy Bypass -File uninstall.ps1 -KeepData  # 只删程序，保留 data/
param([switch]$KeepData)

$ErrorActionPreference = 'SilentlyContinue'
$InstallDir = Split-Path -Parent $MyInvocation.MyCommand.Path

Get-Process -Name 'AetherCockpit' | Stop-Process -Force
Start-Sleep -Milliseconds 500

$startLnk = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Aether Cockpit.lnk'
$deskLnk = Join-Path ([Environment]::GetFolderPath('Desktop')) 'Aether Cockpit.lnk'
Remove-Item -Force $startLnk
Remove-Item -Force $deskLnk
Remove-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'AetherCockpit'
Remove-Item -Recurse -Force 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\AetherCockpit'

if ($KeepData) {
    # 只删程序文件，保留 data/ 与文档
    Get-ChildItem -Path $InstallDir -File | Where-Object { $_.Extension -in '.exe' } | Remove-Item -Force
    Write-Host '程序已卸载，data/ 已保留。'
    return
}

# 自删除：当前脚本就在待删目录里，交给后台 cmd 延迟删除
$target = $InstallDir
$cmd = 'timeout /t 2 >nul & rmdir /s /q "' + $target + '"'
Start-Process -FilePath 'cmd.exe' -ArgumentList '/c', $cmd -WindowStyle Hidden
Write-Host "已卸载。目录将在几秒后删除：$target"
