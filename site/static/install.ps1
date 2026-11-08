# Install Solaria on Windows.
#
#   irm https://solaria.sh/install.ps1 | iex
#
# It downloads the newest release from GitHub (prereleases included while
# Solaria is pre-alpha), checks the archive against the release's
# checksums.txt, copies solaria.exe into your user profile and adds that
# folder to your user PATH. It doesn't need administrator rights.
#
# Settings, all optional:
#   $env:SOLARIA_VERSION      a version such as 0.1.0 (default: the newest release)
#   $env:SOLARIA_INSTALL_DIR  where solaria.exe goes (default: %LOCALAPPDATA%\Programs\Solaria)
#   $env:SOLARIA_DOWNLOAD_URL base URL of the release files, for mirrors

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

& {
  $repo = 'solaria-sh/solaria'
  $installDir = if ($env:SOLARIA_INSTALL_DIR) { $env:SOLARIA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\Solaria' }

  $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "solaria: unsupported processor $($env:PROCESSOR_ARCHITECTURE). Releases are built for amd64 and arm64." }
  }

  $version = $env:SOLARIA_VERSION
  if (-not $version) {
    # The list endpoint, unlike /releases/latest, includes prereleases.
    $releases = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repo/releases?per_page=1"
    if (-not $releases) { throw "solaria: couldn't find a release of $repo. Set `$env:SOLARIA_VERSION to pick one." }
    $version = @($releases)[0].tag_name
  }
  $version = $version.TrimStart('v')

  $base = if ($env:SOLARIA_DOWNLOAD_URL) { $env:SOLARIA_DOWNLOAD_URL } else { "https://github.com/$repo/releases/download/v$version" }
  $archive = "solaria_${version}_windows_${arch}.zip"

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("solaria-" + [Guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "solaria: downloading $archive"
    Invoke-WebRequest -UseBasicParsing "$base/$archive" -OutFile (Join-Path $tmp $archive)
    Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $archive } | Select-Object -First 1
    if (-not $line) { throw "solaria: checksums.txt has no entry for $archive" }
    $expected = ($line -split '\s+')[0].ToLower()
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
    if ($expected -ne $actual) { throw "solaria: checksum mismatch for ${archive}: expected $expected, got $actual" }

    Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    Copy-Item -Force (Join-Path $tmp 'x\solaria.exe') (Join-Path $installDir 'solaria.exe')
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @($userPath -split ';' | Where-Object { $_ })
  if ($parts -notcontains $installDir) {
    [Environment]::SetEnvironmentVariable('Path', (($parts + $installDir) -join ';'), 'User')
    Write-Host "solaria: added $installDir to your user PATH. Open a new terminal to use it."
  }
  Write-Host "solaria: installed $version to $(Join-Path $installDir 'solaria.exe')"
}
