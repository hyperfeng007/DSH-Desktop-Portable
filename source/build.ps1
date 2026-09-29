$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go 1.26 or later from https://go.dev/dl/ first.' }
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go mod download
if ($LASTEXITCODE -ne 0) { throw 'Dependency download failed.' }
go test ./...
if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
go build -trimpath -ldflags '-H=windowsgui -s -w' -o ..\DSH-Portable.exe .
if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
Get-FileHash ..\DSH-Portable.exe -Algorithm SHA256
Write-Host 'Built DSH-Portable.exe. This does not imply native Windows acceptance tests passed.'
