$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$bashScript = Join-Path $scriptDir "build_libdivecomputer_windows.sh"
$repoRoot = (Resolve-Path (Join-Path $scriptDir "..")).Path

if (-not (Test-Path $bashScript)) {
    throw "Missing build script: $bashScript"
}

$bashCandidates = @()
if ($env:BASH_EXE) {
    $bashCandidates += $env:BASH_EXE
}
$bashCandidates += @(
    "C:\msys64\usr\bin\bash.exe",
    "C:\Program Files\Git\bin\bash.exe",
    "C:\Program Files (x86)\Git\bin\bash.exe"
)

$bashExe = $null
foreach ($candidate in $bashCandidates) {
    if ($candidate -and (Test-Path $candidate)) {
        $bashExe = $candidate
        break
    }
}

if (-not $bashExe) {
    throw "Could not find bash.exe. Install MSYS2 or Git Bash, or set BASH_EXE to the full path of bash.exe."
}

$msysRoot = "C:\msys64"
if (Test-Path (Join-Path $msysRoot "usr\bin\bash.exe")) {
    $env:MSYSTEM = "MINGW64"
    $env:CHERE_INVOKING = "1"
    $env:PATH = "$msysRoot\mingw64\bin;$msysRoot\usr\bin;$env:PATH"
}

$env:DIVEVAULT_IMPORTER_ROOT = $repoRoot
Push-Location $repoRoot
try {
    & $bashExe --login $bashScript @args
} finally {
    Pop-Location
}
