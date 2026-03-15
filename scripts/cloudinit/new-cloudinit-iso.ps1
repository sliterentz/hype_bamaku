param(
  [Parameter(Mandatory=$true)][string]$IsoPath,
  [Parameter(Mandatory=$true)][string]$UserDataBase64,
  [Parameter(Mandatory=$true)][string]$MetaDataBase64,
  [Parameter(Mandatory=$true)][string]$NetworkConfigBase64
)

$ErrorActionPreference = 'Stop'

function Ensure-Dir([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) {
    New-Item -ItemType Directory -Path $Path | Out-Null
  }
}

function Write-TextFileUtf8NoBom([string]$Path, [string]$Content) {
  $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
  [System.IO.File]::WriteAllText($Path, $Content, $utf8NoBom)
}

# Gunakan C# Inline untuk bridge IStream yang sangat bandel di PowerShell
$streamBridgeCode = @"
using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;

public class StreamBridge {
    public static void SaveIStreamToFile(object comStream, string filePath) {
        IStream source = (IStream)comStream;
        using (FileStream target = File.Create(filePath)) {
            byte[] buffer = new byte[8192];
            IntPtr bytesReadPtr = Marshal.AllocHGlobal(sizeof(int));
            try {
                while (true) {
                    source.Read(buffer, buffer.Length, bytesReadPtr);
                    int bytesRead = Marshal.ReadInt32(bytesReadPtr);
                    if (bytesRead == 0) break;
                    target.Write(buffer, 0, bytesRead);
                }
            } finally {
                Marshal.FreeHGlobal(bytesReadPtr);
            }
        }
    }
}
"@

Add-Type -TypeDefinition $streamBridgeCode -ErrorAction SilentlyContinue

function Remove-FileWithRetry([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) { return }

  $maxRetries = 5
  $retryCount = 0
  $baseDelay = 1 # seconds

  while ($retryCount -lt $maxRetries) {
    try {
      # Try to rename first as a lock check
      $tempName = $Path + ".deleting." + [Guid]::NewGuid().ToString()
      Rename-Item -LiteralPath $Path -NewName $tempName -ErrorAction Stop
      Remove-Item -LiteralPath $tempName -Force -ErrorAction Stop
      return
    } catch {
      $retryCount++
      if ($retryCount -ge $maxRetries) {
        Write-Error "Failed to remove file '$Path' after $maxRetries attempts. The file might be locked by another process (e.g., Hyper-V VM)."
        throw $_
      }
      
      $delay = $baseDelay * [Math]::Pow(2, $retryCount - 1)
      Write-Warning "File '$Path' is locked. Retrying removal in $delay seconds (Attempt $retryCount/$maxRetries)..."
      Start-Sleep -Seconds $delay
    }
  }
}

function New-IsoFile(
  [Parameter(Mandatory=$true)][string]$Path,
  [Parameter(Mandatory=$true)][string]$SourceDir,
  [string]$VolumeName = 'CIDATA'
) {
  Remove-FileWithRetry -Path $Path

  $fsi = New-Object -ComObject IMAPI2FS.MsftFileSystemImage
  
  try {
    $fsi.FileSystemsToCreate = 1 # ISO9660
    $fsi.VolumeName = $VolumeName
    $fsi.Root.AddTree($SourceDir, $false)

    $result = $fsi.CreateResultImage()
    
    try {
        [StreamBridge]::SaveIStreamToFile($result.ImageStream, $Path)
    } catch {
        Write-Error "Gagal menulis ISO menggunakan StreamBridge: $_"
        throw $_
    }
  } finally {
    if ($fsi) {
      [System.Runtime.InteropServices.Marshal]::ReleaseComObject($fsi) | Out-Null
    }
  }
}

$isoDir = Split-Path -Parent $IsoPath
Ensure-Dir $isoDir

$workDir = Join-Path $isoDir ([System.IO.Path]::GetFileNameWithoutExtension($IsoPath) + '-cidata')
if (Test-Path -LiteralPath $workDir) {
  Remove-Item -LiteralPath $workDir -Recurse -Force
}
Ensure-Dir $workDir

$userData = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($UserDataBase64))
$metaData = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($MetaDataBase64))
$networkConfig = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($NetworkConfigBase64))

$userDataPath = Join-Path $workDir 'user-data'
$metaDataPath = Join-Path $workDir 'meta-data'
$networkConfigPath = Join-Path $workDir 'network-config'

Write-TextFileUtf8NoBom -Path $userDataPath -Content $userData
Write-TextFileUtf8NoBom -Path $metaDataPath -Content $metaData
Write-TextFileUtf8NoBom -Path $networkConfigPath -Content $networkConfig

# Coba oscdimg dulu karena lebih reliable
$oscdimg = Get-Command oscdimg.exe -ErrorAction SilentlyContinue
if ($oscdimg) {
    Remove-FileWithRetry -Path $IsoPath
    & $oscdimg.Path -n -m -lcidata "$workDir" "$IsoPath"
    if ($LASTEXITCODE -ne 0) {
      throw "oscdimg gagal dengan exit code $LASTEXITCODE"
    }
} else {
    New-IsoFile -Path $IsoPath -SourceDir $workDir
}

if (-not (Test-Path -LiteralPath $IsoPath)) {
  throw "ISO tidak berhasil dibuat: $IsoPath"
}

if (Test-Path -LiteralPath $workDir) {
  Remove-Item -LiteralPath $workDir -Recurse -Force
}

Write-Output $IsoPath
