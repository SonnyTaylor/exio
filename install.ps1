# Exio installer script for Windows
# Usage: irm https://raw.githubusercontent.com/SonnyTaylor/exio/main/install.ps1 | iex

$ErrorActionPreference = "Stop"

$Repo = "SonnyTaylor/exio"
$BinaryName = "exio"

function Write-Info {
    param([string]$Message)
    Write-Host "==> " -ForegroundColor Blue -NoNewline
    Write-Host $Message
}

function Write-Success {
    param([string]$Message)
    Write-Host "==> " -ForegroundColor Green -NoNewline
    Write-Host $Message
}

function Write-Warn {
    param([string]$Message)
    Write-Host "Warning: " -ForegroundColor Yellow -NoNewline
    Write-Host $Message
}

function Write-Error-Custom {
    param([string]$Message)
    Write-Host "Error: " -ForegroundColor Red -NoNewline
    Write-Host $Message
    exit 1
}

function Get-Architecture {
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { Write-Error-Custom "Unsupported architecture: $arch" }
    }
}

function Get-LatestVersion {
    try {
        $response = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        return $response.tag_name
    }
    catch {
        Write-Error-Custom "Failed to fetch latest version: $_"
    }
}

function Get-Checksum {
    param(
        [string]$ChecksumFile,
        [string]$FileName
    )
    
    $content = Get-Content $ChecksumFile
    foreach ($line in $content) {
        if ($line -match "^([a-f0-9]+)\s+.*$FileName") {
            return $matches[1]
        }
    }
    return $null
}

function Install-Exio {
    Write-Host ""
    Write-Host "  +-----------------------------------+" -ForegroundColor Cyan
    Write-Host "  |       Exio Installer              |" -ForegroundColor Cyan
    Write-Host "  |   High-performance tunneling      |" -ForegroundColor Cyan
    Write-Host "  +-----------------------------------+" -ForegroundColor Cyan
    Write-Host ""

    # Detect architecture
    $arch = Get-Architecture
    Write-Info "Detected architecture: windows/$arch"

    # Get latest version
    Write-Info "Fetching latest version..."
    $version = Get-LatestVersion
    Write-Info "Latest version: $version"

    # Construct URLs
    $binaryFile = "$BinaryName-windows-$arch.exe"
    $zipFile = "$BinaryName-windows-$arch.zip"
    $downloadUrl = "https://github.com/$Repo/releases/download/$version/$zipFile"
    $checksumUrl = "https://github.com/$Repo/releases/download/$version/checksums.txt"

    # Create temp directory
    $tempDir = Join-Path $env:TEMP "exio-install-$(Get-Random)"
    New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

    try {
        # Download zip
        Write-Info "Downloading $zipFile..."
        $zipPath = Join-Path $tempDir $zipFile
        Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath -UseBasicParsing

        # Download checksums
        Write-Info "Verifying checksum..."
        $checksumPath = Join-Path $tempDir "checksums.txt"
        Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath -UseBasicParsing

        # Verify checksum
        $expectedChecksum = Get-Checksum -ChecksumFile $checksumPath -FileName $zipFile
        if ($expectedChecksum) {
            $actualChecksum = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
            if ($actualChecksum -ne $expectedChecksum) {
                Write-Error-Custom "Checksum verification failed!`nExpected: $expectedChecksum`nActual: $actualChecksum"
            }
            Write-Success "Checksum verified"
        }
        else {
            Write-Warn "Could not find checksum for $zipFile, skipping verification"
        }

        # Extract
        Write-Info "Extracting..."
        Expand-Archive -Path $zipPath -DestinationPath $tempDir -Force

        # Determine install location
        $installDir = Join-Path $env:LOCALAPPDATA "Programs\Exio"
        if (-not (Test-Path $installDir)) {
            New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        }

        # Install
        Write-Info "Installing to $installDir..."
        $sourcePath = Join-Path $tempDir $binaryFile
        $destPath = Join-Path $installDir "$BinaryName.exe"
        Move-Item -Path $sourcePath -Destination $destPath -Force

        # Add to PATH if not already there
        $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
        if ($userPath -notlike "*$installDir*") {
            Write-Info "Adding to PATH..."
            [Environment]::SetEnvironmentVariable("PATH", "$userPath;$installDir", "User")
            $env:PATH = "$env:PATH;$installDir"
        }

        Write-Success "Exio installed successfully!"
        Write-Host ""
        Write-Host "  Version: $version" -ForegroundColor White
        Write-Host "  Location: $destPath" -ForegroundColor White
        Write-Host ""
        Write-Host "  Get started:" -ForegroundColor White
        Write-Host "    exio init              # Configure your connection" -ForegroundColor Gray
        Write-Host "    exio http 3000         # Expose port 3000" -ForegroundColor Gray
        Write-Host ""
        Write-Warn "Restart your terminal for PATH changes to take effect"
    }
    finally {
        # Cleanup
        if (Test-Path $tempDir) {
            Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

# Run installer
Install-Exio
