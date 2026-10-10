$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
docker build --target artifact --output type=local,dest=. .
if ($LASTEXITCODE -ne 0) { throw 'Docker build failed' }
if (!(Test-Path LANPrint.exe)) { throw 'LANPrint.exe missing' }
$expected = ((Get-Content LANPrint.exe.sha256) -split '\s+')[0]
$actual = (Get-FileHash LANPrint.exe -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'SHA-256 mismatch' }
Write-Host "LANPrint.exe saved to $PSScriptRoot"
