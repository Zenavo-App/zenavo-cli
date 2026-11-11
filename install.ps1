# Installs the Zenavo CLI on Windows:
#
#   irm https://zenavo.app/install.ps1 | iex
#
# It downloads the binary for this system from a release of github.com/Zenavo-App/zenavo-cli, checks it against
# the release's SHA256SUMS, installs it to %LOCALAPPDATA%\Programs\zenavo and adds that to your user PATH. Nothing
# needs administrator rights. Settings, all optional:
#
#   $env:ZENAVO_VERSION       a release tag such as v0.2.0 (default: the latest release)
#   $env:ZENAVO_INSTALL_DIR   the directory to install zenavo.exe into
#   $env:ZENAVO_DOWNLOAD_URL  where the release files are, for a mirror (default: the GitHub release)
#
# Everything is inside one script block, run on the last line, so a download cut short runs nothing.

& {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue' # the progress bar makes Invoke-WebRequest far slower on Windows PowerShell
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	# A 32-bit PowerShell on 64-bit Windows reports x86 here and the real processor in PROCESSOR_ARCHITEW6432.
	$cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
	$arch = switch ($cpu) {
		'AMD64' { 'amd64' }
		'ARM64' { 'arm64' }
		default { throw "zenavo install: no build for this processor ($cpu)" }
	}

	$base = $env:ZENAVO_DOWNLOAD_URL
	if (-not $base) {
		if ($env:ZENAVO_VERSION) { $base = "https://github.com/Zenavo-App/zenavo-cli/releases/download/$env:ZENAVO_VERSION" }
		else { $base = 'https://github.com/Zenavo-App/zenavo-cli/releases/latest/download' }
	}
	$base = $base.TrimEnd('/')

	$file = "zenavo_windows_$arch.exe"
	$tmp = Join-Path ([IO.Path]::GetTempPath()) ('zenavo-' + [Guid]::NewGuid())
	New-Item -ItemType Directory -Path $tmp | Out-Null
	try {
		$download = Join-Path $tmp $file
		$sums = Join-Path $tmp 'SHA256SUMS'
		Invoke-WebRequest -UseBasicParsing -Uri "$base/$file" -OutFile $download
		Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums

		$want = $null
		foreach ($line in Get-Content $sums) {
			$parts = $line.Trim() -split '\s+'
			if ($parts.Count -ge 2 -and ($parts[1] -eq $file -or $parts[1] -eq "*$file")) { $want = $parts[0].ToLower(); break }
		}
		if (-not $want) { throw "zenavo install: $file is not listed in SHA256SUMS" }
		$got = (Get-FileHash -Algorithm SHA256 -Path $download).Hash.ToLower()
		if ($got -ne $want) { throw "zenavo install: the download does not match SHA256SUMS (expected $want, got $got); nothing was installed" }

		$dir = $env:ZENAVO_INSTALL_DIR
		if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\zenavo' }
		New-Item -ItemType Directory -Force -Path $dir | Out-Null
		$exe = Join-Path $dir 'zenavo.exe'
		Move-Item -Force -Path $download -Destination $exe

		Write-Host "Installed $(& $exe version) to $exe"
		$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
		if (-not $userPath) { $userPath = '' }
		if (($userPath -split ';') -notcontains $dir) {
			[Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';'), $dir) -join ';').TrimStart(';'), 'User')
			$env:Path = "$env:Path;$dir"
			Write-Host "Added $dir to your PATH. New terminals will find zenavo; this one already does."
		}
		Write-Host 'Get started: zenavo auth login'
	}
	finally {
		Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
	}
}
