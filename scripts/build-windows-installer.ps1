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

    $binDir = Join-Path $repoRoot "build/bin"
    $installers = @(Get-ChildItem -LiteralPath $binDir -Filter "*installer*.exe" -File)
    if ($installers.Count -ne 1) {
        throw "Expected exactly one NSIS installer in '$binDir', found $($installers.Count)."
    }

    $installer = $installers[0].FullName
    $hash = Get-FileHash -LiteralPath $installer -Algorithm SHA256
    Write-Host "Installer: $installer"
    Write-Host "SHA256: $($hash.Hash)"
}
finally {
    Pop-Location
}
