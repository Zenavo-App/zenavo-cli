# Runs install.ps1 against the release files in dist/ (make dist), served over HTTP on this machine: it must install
# a working zenavo.exe and put its directory on the user PATH, and must refuse a download that does not match
# SHA256SUMS. CI runs it on Windows; it needs Python for the local server.

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $root 'dist/SHA256SUMS'))) { throw 'run make dist first' }
$work = Join-Path ([IO.Path]::GetTempPath()) ('zenavo-test-' + [Guid]::NewGuid())
$release = Join-Path $work 'release'
New-Item -ItemType Directory -Path $release | Out-Null
Copy-Item (Join-Path $root 'dist/*') $release

$port = Get-Random -Minimum 20000 -Maximum 40000
$python = if (Get-Command python3 -ErrorAction SilentlyContinue) { 'python3' } else { 'python' }
$server = Start-Process -PassThru -NoNewWindow -FilePath $python -ArgumentList '-m', 'http.server', $port, '--bind', '127.0.0.1', '--directory', $release
$savedPath = [Environment]::GetEnvironmentVariable('Path', 'User')
try {
	Start-Sleep -Seconds 2
	$env:ZENAVO_DOWNLOAD_URL = "http://127.0.0.1:$port"

	# A good release installs and lands on the user PATH.
	$env:ZENAVO_INSTALL_DIR = Join-Path $work 'bin'
	& (Join-Path $root 'install.ps1')
	$version = & (Join-Path $env:ZENAVO_INSTALL_DIR 'zenavo.exe') version
	if ($version -notmatch '^zenavo ') { throw "FAIL: zenavo version printed '$version'" }
	if (([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') -notcontains $env:ZENAVO_INSTALL_DIR) { throw 'FAIL: not added to the user PATH' }

	# A binary that does not match its checksum is refused and nothing is installed.
	$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
	Add-Content -Path (Join-Path $release "zenavo_windows_$arch.exe") -Value 'tampered'
	$env:ZENAVO_INSTALL_DIR = Join-Path $work 'bad'
	$refused = $false
	try { & (Join-Path $root 'install.ps1') } catch { $refused = $_.Exception.Message -match 'does not match SHA256SUMS' }
	if (-not $refused) { throw 'FAIL: a tampered binary was not refused' }
	if (Test-Path (Join-Path $env:ZENAVO_INSTALL_DIR 'zenavo.exe')) { throw 'FAIL: something was written for a tampered binary' }
	Write-Host 'install.ps1: ok'
}
finally {
	[Environment]::SetEnvironmentVariable('Path', $savedPath, 'User')
	Remove-Item Env:ZENAVO_DOWNLOAD_URL, Env:ZENAVO_INSTALL_DIR -ErrorAction SilentlyContinue
	Stop-Process -Id $server.Id -ErrorAction SilentlyContinue
	Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
