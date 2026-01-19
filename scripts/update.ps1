# Exio Update Script
# Run from the exio repository root

param(
    [switch]$Client,
    [switch]$Server,
    [switch]$All
)

$ErrorActionPreference = "Stop"

if (-not $Client -and -not $Server -and -not $All) {
    $All = $true
}

$RepoRoot = $PSScriptRoot | Split-Path -Parent
Push-Location $RepoRoot

try {
    Write-Host "Pulling latest changes..." -ForegroundColor Cyan
    git pull

    if ($All -or $Client) {
        Write-Host "`nBuilding client (Windows)..." -ForegroundColor Cyan
        $env:GOOS = "windows"
        $env:GOARCH = "amd64"
        go build -o exio.exe ./cmd/exio
        
        Write-Host "Installing client..." -ForegroundColor Cyan
        Copy-Item exio.exe "$env:LOCALAPPDATA\Programs\Exio\exio.exe" -Force
        Write-Host "Client updated!" -ForegroundColor Green
    }

    if ($All -or $Server) {
        Write-Host "`nBuilding server (Linux)..." -ForegroundColor Cyan
        $env:GOOS = "linux"
        $env:GOARCH = "amd64"
        go build -o exiod-linux-amd64 ./cmd/exiod

        Write-Host "Deploying to server..." -ForegroundColor Cyan
        scp exiod-linux-amd64 proxmox:/tmp/exiod
        ssh proxmox "pct push 134 /tmp/exiod /usr/local/bin/exiod && pct exec 134 -- chmod +x /usr/local/bin/exiod && pct exec 134 -- systemctl restart exiod"
        
        Write-Host "Server updated!" -ForegroundColor Green
        
        Write-Host "`nServer status:" -ForegroundColor Cyan
        ssh proxmox "pct exec 134 -- systemctl status exiod --no-pager"
    }

    Write-Host "`nUpdate complete!" -ForegroundColor Green
}
finally {
    Pop-Location
}
