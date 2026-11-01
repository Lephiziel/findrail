$ErrorActionPreference = 'Stop'
$DestinationDirectory = if ($args.Count -gt 0) { $args[0] } else { [Environment]::GetFolderPath('Programs') }
$shortcutPath = Join-Path $DestinationDirectory 'Findrail.lnk'
if (Test-Path -LiteralPath $shortcutPath) {
  $shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcutPath)
  if ($shortcut.Description -ne 'Findrail managed shortcut' -and
      ([IO.Path]::GetFileName($shortcut.TargetPath) -ne 'findrail-desktop.exe' -or
       [IO.Path]::GetFileName($shortcut.WorkingDirectory) -notlike 'findrail_*')) {
    throw 'Refusing to remove an unowned shortcut.'
  }
  Remove-Item -LiteralPath $shortcutPath -Force
}
Write-Output 'Removed Findrail shortcut. User data was not changed.'
