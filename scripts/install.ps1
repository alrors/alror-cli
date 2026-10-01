# Install the alror CLI on Windows from GitHub releases.
#   irm https://raw.githubusercontent.com/manaskumar3003/alror-cli/main/scripts/install.ps1 | iex
# Env: ALROR_VERSION (default: latest), ALROR_INSTALL_DIR (default: %LOCALAPPDATA%\alror\bin)
$ErrorActionPreference = 'Stop'
$repo = 'manaskumar3003/alror-cli'
$version = if ($env:ALROR_VERSION) { $env:ALROR_VERSION } else { 'latest' }
if ($version -eq 'latest') {
  $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
}
$num = $version.TrimStart('v')
$arch = if ([Environment]::Is64BitOperatingSystem) { if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' } } else { throw '32-bit Windows is not supported' }
$archive = "alror_${num}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/$version"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("alror-" + [guid]::NewGuid())
New-Item -ItemType Directory $tmp | Out-Null
try {
  Write-Host "Downloading alror $version for windows/$arch..."
  Invoke-WebRequest "$base/$archive" -OutFile "$tmp\$archive" -UseBasicParsing
  Invoke-WebRequest "$base/checksums.txt" -OutFile "$tmp\checksums.txt" -UseBasicParsing
  $expected = (Get-Content "$tmp\checksums.txt" | Where-Object { $_ -match " $([regex]::Escape($archive))$" }) -split '\s+' | Select-Object -First 1
  $actual = (Get-FileHash "$tmp\$archive" -Algorithm SHA256).Hash.ToLower()
  if ($expected -ne $actual) { throw "checksum mismatch for $archive" }
  Expand-Archive "$tmp\$archive" -DestinationPath $tmp -Force
  $dir = if ($env:ALROR_INSTALL_DIR) { $env:ALROR_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'alror\bin' }
  New-Item -ItemType Directory -Force $dir | Out-Null
  Copy-Item "$tmp\alror.exe" $dir -Force
  if (Test-Path "$tmp\alror-docs.exe") { Copy-Item "$tmp\alror-docs.exe" $dir -Force }
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', "$dir;$userPath", 'User')
    Write-Host "Added $dir to your user PATH (open a new terminal)."
  }
  Write-Host "Installed alror to $dir\alror.exe"
  & "$dir\alror.exe" version
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
