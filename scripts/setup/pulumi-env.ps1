param(
  [ValidateSet('cloud','local')][string]$Backend = 'local',
  [string]$Stack = 'dev',
  [switch]$CI,
  [string]$PulumiHome,
  [string]$AccessToken,
  [string]$Passphrase,
  [string]$PassphraseFile
)

$ErrorActionPreference = 'Stop'

function To-FileUrl([string]$Path) {
  $full = (Resolve-Path -LiteralPath $Path).Path
  $full = $full -replace '\\', '/'
  if ($full -match '^[A-Za-z]:/') {
    return "file://$full"
  }
  return "file://$full"
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path

# 1. Load from .env file if it exists for the stack
$envFile = Join-Path $repoRoot ".env.$Stack"
if (Test-Path $envFile) {
  Write-Host "Loading environment from $envFile" -ForegroundColor Cyan
  Get-Content $envFile | Where-Object { $_ -match '=' -and $_ -notmatch '^#' } | ForEach-Object {
    $parts = $_.Split('=', 2)
    $key = $parts[0].Trim()
    $value = $parts[1].Trim()
    # Remove quotes if present
    $value = $value -replace '^["'']|["'']$', ''
    if ($key -and $value) {
      Set-Content "env:$key" $value
    }
  }
}

# 2. Set PULUMI_HOME
if (-not $PulumiHome -or $PulumiHome.Trim() -eq '') {
  $PulumiHome = (Join-Path $repoRoot '.pulumi-home')
}
New-Item -ItemType Directory -Force -Path $PulumiHome | Out-Null
$env:PULUMI_HOME = $PulumiHome

$env:PULUMI_SKIP_UPDATE_CHECK = 'true'

if ($Stack -and $Stack.Trim() -ne '') {
  $env:PULUMI_STACK = $Stack
}

# 3. Handle Backend
if ($Backend -eq 'local') {
  $stateDir = Join-Path $repoRoot '.pulumi-state'
  if (-not (Test-Path $stateDir)) {
    New-Item -ItemType Directory -Force -Path $stateDir | Out-Null
  }
  $env:PULUMI_BACKEND_URL = (To-FileUrl $stateDir)
}

# 4. Handle Passphrase Priority: 
#   Priority: Parameter -Passphrase > Parameter -PassphraseFile > Environment Variable (from .env)

if ($Passphrase -and $Passphrase.Trim() -ne '') {
  $env:PULUMI_CONFIG_PASSPHRASE = $Passphrase
  Remove-Item -Path env:PULUMI_CONFIG_PASSPHRASE_FILE -ErrorAction SilentlyContinue
  Write-Host "Using passphrase from parameter" -ForegroundColor Green
}
elseif ($PassphraseFile -and $PassphraseFile.Trim() -ne '') {
  if (Test-Path $PassphraseFile) {
    $env:PULUMI_CONFIG_PASSPHRASE_FILE = (Resolve-Path $PassphraseFile).Path
    $env:PULUMI_CONFIG_PASSPHRASE = "" # Clear plain text if using file
    Write-Host "Using passphrase file: $PassphraseFile" -ForegroundColor Green
  } else {
    Write-Warning "Passphrase file not found: $PassphraseFile"
  }
}
elseif ($env:PULUMI_CONFIG_PASSPHRASE_FILE) {
    # If already set via .env but file doesn't exist, try to resolve it relative to repo root
    if (-not (Test-Path $env:PULUMI_CONFIG_PASSPHRASE_FILE)) {
        $resolvedPath = Join-Path $repoRoot $env:PULUMI_CONFIG_PASSPHRASE_FILE
        if (Test-Path $resolvedPath) {
            $env:PULUMI_CONFIG_PASSPHRASE_FILE = $resolvedPath
        }
    }
    if (Test-Path $env:PULUMI_CONFIG_PASSPHRASE_FILE) {
        $env:PULUMI_CONFIG_PASSPHRASE = "" # Ensure plain text is empty
        Write-Host "Using passphrase file from .env: $($env:PULUMI_CONFIG_PASSPHRASE_FILE)" -ForegroundColor Green
    } else {
        Write-Warning "PULUMI_CONFIG_PASSPHRASE_FILE in .env points to non-existent file: $($env:PULUMI_CONFIG_PASSPHRASE_FILE)"
    }
}
elseif ($env:PULUMI_CONFIG_PASSPHRASE) {
    Write-Host "Using passphrase from .env" -ForegroundColor Green
}
else {
  Write-Warning "No passphrase found in parameters, .env, or file. Pulumi may prompt for one."
}

# 5. Select Stack and Login
if ($Backend -eq 'local') {
  Write-Host "Logging into local backend: $($env:PULUMI_BACKEND_URL)" -ForegroundColor Cyan
  pulumi login $env:PULUMI_BACKEND_URL --non-interactive
  
  if ($Stack) {
    Write-Host "Selecting stack: $Stack" -ForegroundColor Cyan
    # Select stack in the specific backend
    pulumi stack select $Stack --non-interactive
  }
}
elseif ($Backend -eq 'cloud') {
  if ($AccessToken) {
    Write-Host "Logging into Pulumi Cloud..." -ForegroundColor Cyan
    pulumi login --non-interactive
  }
  if ($Stack) {
    pulumi stack select $Stack --non-interactive
  }
}

if ($CI) {
  $env:PULUMI_CI = 'true'
  $env:PULUMI_OPTION_NON_INTERACTIVE = 'true'
  $env:PULUMI_OPTION_YES = 'true'
  $env:PULUMI_OPTION_SKIP_PREVIEW = 'true'
}

# Output current configuration
Write-Output "--- Current Pulumi Environment ---"
Write-Output "PULUMI_HOME:          $($env:PULUMI_HOME)"
Write-Output "PULUMI_BACKEND_URL:   $($env:PULUMI_BACKEND_URL)"
Write-Output "PULUMI_STACK:         $($env:PULUMI_STACK)"
if ($env:PULUMI_CONFIG_PASSPHRASE) { 
    Write-Output "PULUMI_CONFIG_PASSPHRASE: [SET]" 
}
if ($env:PULUMI_CONFIG_PASSPHRASE_FILE) { 
    Write-Output "PULUMI_CONFIG_PASSPHRASE_FILE: $($env:PULUMI_CONFIG_PASSPHRASE_FILE)" 
}
Write-Output "PULUMI_SKIP_UPDATE_CHECK: $($env:PULUMI_SKIP_UPDATE_CHECK)"
if ($env:PULUMI_CI) { Write-Output "PULUMI_CI: true" }
Write-Output "----------------------------------"
