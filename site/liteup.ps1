# liteup.ps1: installs the lite-deploy client on Windows (for the web interface).
#
#   irm https://lite-deploy.prozorov.me/liteup.ps1 | iex
#
# It downloads lite-deploy.exe from the latest GitHub release into %LOCALAPPDATA%\lite-deploy, checks its
# SHA256 against the signed SHA256SUMS of the same release (and the minisign signature too, if minisign
# is installed), and adds the folder to your user PATH. The server side is installed on Linux with liteup.
#
# Options (environment variables): LITE_DEPLOY_VERSION (a tag like v0.6.2, the latest by default),
# LITE_DEPLOY_BASE_URL (for testing), LITE_DEPLOY_SKIP_SIG=1 (do not use minisign even if present),
# LITE_DEPLOY_NO_PATH=1 (do not touch your PATH).
$ErrorActionPreference = 'Stop'

# The minisign public key the releases are signed with. Matches minisign.pub in the repository.
$MinisignPubKey = 'RWQXkl8IozvEWfo230NRtIvfuO4KI+SiT2s0p+Qynna/9WSATC/JDcyJ'
$Repo = 'toxic-sunflower/lite-deploy'

function Fail($msg) { Write-Host "ERROR: $msg" -ForegroundColor Red; throw $msg }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "architecture $($env:PROCESSOR_ARCHITECTURE) is not supported (amd64 and arm64 only)" }
}

$version = if ($env:LITE_DEPLOY_VERSION) { $env:LITE_DEPLOY_VERSION } else { 'latest' }
if ($version -ne 'latest' -and $version -notmatch '^v[0-9]') { Fail 'the version must look like v1.2.3 or latest' }

if ($env:LITE_DEPLOY_BASE_URL) { $base = $env:LITE_DEPLOY_BASE_URL }
elseif ($version -eq 'latest') { $base = "https://github.com/$Repo/releases/latest/download" }
else { $base = "https://github.com/$Repo/releases/download/$version" }

$name = "lite-deploy-windows-$arch.exe"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("liteup-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "[1/3] Downloading $name ($version)"
    Invoke-WebRequest -UseBasicParsing "$base/$name" -OutFile (Join-Path $tmp $name)
    Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')

    $note = ''
    $minisign = Get-Command minisign -ErrorAction SilentlyContinue
    if ($minisign -and $env:LITE_DEPLOY_SKIP_SIG -ne '1') {
        Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS.minisig" -OutFile (Join-Path $tmp 'SHA256SUMS.minisig')
        & minisign -Vm (Join-Path $tmp 'SHA256SUMS') -P $MinisignPubKey -x (Join-Path $tmp 'SHA256SUMS.minisig') | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail 'the SHA256SUMS signature is invalid: the files may have been tampered with, installation cancelled' }
        $note = 'signature OK, '
    } else {
        Write-Host 'Note: minisign is not installed, so only the SHA256 from the release is checked. Install minisign to verify the signature too.'
    }

    $line = Get-Content (Join-Path $tmp 'SHA256SUMS') | Where-Object { $_ -match "\s\*?$([regex]::Escape($name))$" } | Select-Object -First 1
    if (-not $line) { Fail "SHA256SUMS has no entry for $name" }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $name)).Hash.ToLower()
    if ($want -ne $got) { Fail "checksum mismatch (expected $want, got $got)" }
    Write-Host "[2/3] Verified: ${note}SHA256 OK"

    $dir = Join-Path $env:LOCALAPPDATA 'lite-deploy'
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item (Join-Path $tmp $name) (Join-Path $dir 'lite-deploy.exe') -Force
    Copy-Item (Join-Path $tmp $name) (Join-Path $dir 'lite.exe') -Force

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($env:LITE_DEPLOY_NO_PATH -eq '1') {
        Write-Host "[3/3] Installed $dir\lite-deploy.exe"
    } elseif (($userPath -split ';') -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dir), 'User')
        $env:Path += ";$dir"
        Write-Host "[3/3] Installed $dir\lite-deploy.exe and added the folder to your PATH (open a new terminal to use it)"
    } else {
        Write-Host "[3/3] Installed $dir\lite-deploy.exe"
    }
    Write-Host "Done: $(& (Join-Path $dir 'lite-deploy.exe') version)."
    Write-Host 'Next: lite-deploy ui --host YOUR_USER@YOUR_SERVER   (the web interface; needs the OpenSSH client and a key login to the server)'
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
