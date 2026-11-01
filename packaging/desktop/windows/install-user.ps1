param([string]$Binary = (Join-Path $PSScriptRoot '..\..\findrail-desktop.exe'), [string]$DestinationDirectory)
$ErrorActionPreference = 'Stop'
$programs = if ($DestinationDirectory) { $DestinationDirectory } else { [Environment]::GetFolderPath('Programs') }
New-Item -ItemType Directory -Path $programs -Force | Out-Null
$shortcutPath = Join-Path $programs 'Findrail.lnk'
$resolvedBinary = (Resolve-Path -LiteralPath $Binary).Path
if (Test-Path -LiteralPath $shortcutPath) {
  $existing = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcutPath)
  if ($existing.Description -ne 'Findrail managed shortcut' -and
      ([IO.Path]::GetFileName($existing.TargetPath) -ne 'findrail-desktop.exe' -or
       [IO.Path]::GetFileName($existing.WorkingDirectory) -notlike 'findrail_*')) {
    throw 'Refusing to replace an existing unowned Findrail shortcut.'
  }
}
$staged = Join-Path $programs ('.Findrail.' + [guid]::NewGuid().ToString('N') + '.lnk')
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($staged)
$shortcut.TargetPath = $resolvedBinary
$shortcut.WorkingDirectory = Split-Path -Parent $resolvedBinary
$shortcut.Description = 'Findrail managed shortcut'
$shortcut.Save()
if (Test-Path -LiteralPath $shortcutPath) {
  $backup = Join-Path $programs ('.Findrail.previous.' + [guid]::NewGuid().ToString('N') + '.lnk')
  [IO.File]::Replace($staged, $shortcutPath, $backup)
  Remove-Item -LiteralPath $backup -Force
} else {
  Move-Item -LiteralPath $staged -Destination $shortcutPath
}
Write-Output "Installed user shortcut: $shortcutPath"
