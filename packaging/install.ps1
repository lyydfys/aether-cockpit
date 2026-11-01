# Aether Cockpit 安装脚本（用户级，无需管理员）
#
# 用法（在发布包根目录运行）：
#   powershell -ExecutionPolicy Bypass -File installer\install.ps1
#   powershell -ExecutionPolicy Bypass -File installer\install.ps1 -AutoStart
#   powershell -ExecutionPolicy Bypass -File installer\install.ps1 -NoStartMenu -NoDesktopShortcut
#
# 安装位置默认 %LOCALAPPDATA%\Programs\AetherCockpit；数据默认在该目录的 data/。
param(
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\AetherCockpit'),
    [switch]$NoStartMenu,
    [switch]$NoDesktopShortcut,
    [switch]$AutoStart
)

$ErrorActionPreference = 'Stop'
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$pkgRoot = Split-Path -Parent $scriptDir

$exeSrc = Join-Path $pkgRoot 'AetherCockpit.exe'
if (-not (Test-Path $exeSrc)) {
    throw "找不到 AetherCockpit.exe：请从发布包解压后的根目录运行 installer\install.ps1"
}

$exeDir = Join-Path $InstallDir ''
$exeDst = Join-Path $InstallDir 'AetherCockpit.exe'

# 运行中的实例先退出，否则 exe 被占用无法覆盖
Get-Process -Name 'AetherCockpit' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 400

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item -Force (Join-Path $pkgRoot '*.exe') $InstallDir
foreach ($f in @('README.md', 'CHANGELOG.md', 'NOTICE', 'VERSION')) {
    $p = Join-Path $pkgRoot $f
    if (Test-Path $p) { Copy-Item -Force $p $InstallDir }
}
$docsSrc = Join-Path $pkgRoot 'docs'
if (Test-Path $docsSrc) {
    New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir 'docs') | Out-Null
    Copy-Item -Force (Join-Path $docsSrc '*') (Join-Path $InstallDir 'docs')
}
Copy-Item -Force (Join-Path $scriptDir 'uninstall.ps1') $InstallDir

function New-Shortcut([string]$Path, [string]$Target) {
    $shell = New-Object -ComObject WScript.Shell
    $lnk = $shell.CreateShortcut($Path)
    $lnk.TargetPath = $Target
    $lnk.WorkingDirectory = (Split-Path -Parent $Target)
    $lnk.IconLocation = "$Target,0"
    $lnk.Description = 'Aether Cockpit · AI 算力聚合网关'
    $lnk.Save()
}

if (-not $NoStartMenu) {
    $startDir = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
    New-Item -ItemType Directory -Force -Path $startDir | Out-Null
    New-Shortcut (Join-Path $startDir 'Aether Cockpit.lnk') $exeDst
}
if (-not $NoDesktopShortcut) {
    $desktop = [Environment]::GetFolderPath('Desktop')
    New-Shortcut (Join-Path $desktop 'Aether Cockpit.lnk') $exeDst
}

$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
if ($AutoStart) {
    New-Item -Force -Path $runKey | Out-Null
    Set-ItemProperty -Path $runKey -Name 'AetherCockpit' -Value ('"' + $exeDst + '"')
} else {
    Remove-ItemProperty -Path $runKey -Name 'AetherCockpit' -ErrorAction SilentlyContinue
}

# 控制面板「应用和功能」条目（当前用户）
$arpKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\AetherCockpit'
New-Item -Force -Path $arpKey | Out-Null
$ver = '0.0.0'
$verFile = Join-Path $pkgRoot 'VERSION'
if (Test-Path $verFile) { $ver = (Get-Content -Raw $verFile).Trim() }
Set-ItemProperty -Path $arpKey -Name 'DisplayName' -Value 'Aether Cockpit'
Set-ItemProperty -Path $arpKey -Name 'DisplayVersion' -Value $ver
Set-ItemProperty -Path $arpKey -Name 'Publisher' -Value 'Aether Cockpit'
Set-ItemProperty -Path $arpKey -Name 'InstallLocation' -Value $InstallDir
Set-ItemProperty -Path $arpKey -Name 'DisplayIcon' -Value $exeDst
Set-ItemProperty -Path $arpKey -Name 'UninstallString' -Value ('powershell -ExecutionPolicy Bypass -File "' + (Join-Path $InstallDir 'uninstall.ps1') + '"')
Set-ItemProperty -Path $arpKey -Name 'NoModify' -Value 1 -Type DWord
Set-ItemProperty -Path $arpKey -Name 'NoRepair' -Value 1 -Type DWord

Write-Host ''
Write-Host "Aether Cockpit $ver 已安装到：$InstallDir"
Write-Host '启动：开始菜单 → Aether Cockpit（或安装目录下的 AetherCockpit.exe）'
if ($AutoStart) { Write-Host '已设置开机自启（登录时启动）。' }
Write-Host '卸载：设置 → 应用 → Aether Cockpit，或运行安装目录下的 uninstall.ps1'
Write-Host '数据默认保存在安装目录的 data/；删除前请先退出应用。'
