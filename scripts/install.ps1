$ErrorActionPreference = "Stop"

$ownerRepo = "1001encore/wave"
$asset = "wave_windows_amd64.zip"
$url = "https://github.com/$ownerRepo/releases/latest/download/$asset"

$installDir = Join-Path $env:LOCALAPPDATA "wave"
$tmpZip = Join-Path $env:TEMP $asset

Write-Host "Downloading $url"
Invoke-WebRequest -Uri $url -OutFile $tmpZip

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
Expand-Archive -Path $tmpZip -DestinationPath $installDir -Force
Remove-Item $tmpZip -Force

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ([string]::IsNullOrWhiteSpace($userPath)) {
  $userPath = $installDir
} elseif (-not (($userPath -split ";") -contains $installDir)) {
  $userPath = "$userPath;$installDir"
}
[Environment]::SetEnvironmentVariable("Path", $userPath, "User")

if (-not (($env:Path -split ";") -contains $installDir)) {
  $env:Path = "$env:Path;$installDir"
}

Write-Host "Installed wave to $installDir\wave.exe"
& (Join-Path $installDir "wave.exe") --help | Out-Null
Write-Host "Install complete."
Write-Host "If needed, open a new terminal so PATH changes are picked up."
