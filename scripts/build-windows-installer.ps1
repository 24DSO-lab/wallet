$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    foreach ($tool in @("wails", "makensis")) {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
            throw "Required tool '$tool' was not found in PATH. Install Wails CLI 2.17.0 and NSIS first."
        }
    }

    & wails build -clean -platform windows/amd64 -nsis -webview2 download
    if ($LASTEXITCODE -ne 0) {
        throw "Wails Windows installer build failed with exit code $LASTEXITCODE."
    }

    $installer = Join-Path $repoRoot "build/bin/wallet-amd64-installer.exe"
    if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
        throw "Wails did not produce the expected NSIS installer: $installer"
    }

    $hash = Get-FileHash -LiteralPath $installer -Algorithm SHA256
    Write-Host "Installer: $installer"
    Write-Host "SHA256: $($hash.Hash)"
}
finally {
    Pop-Location
}
