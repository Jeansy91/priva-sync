param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("Install", "PrepareInstall", "Remove")]
    [string] $Action
)

$ErrorActionPreference = "Stop"
$packageName = "PrivaSync.Shell"
$certificateRegistrySubKey = "Software\PrivaSync\ExplorerIdentity\Certificates"
$installDir = [System.IO.Path]::GetFullPath($PSScriptRoot)
$shellDll = Join-Path $installDir "privasync_shell.dll"
$packagePath = Join-Path $installDir "privasync-identity.msix"
$certificatePath = Join-Path $installDir "privasync-package.cer"
$elevatedScript = Join-Path $installDir "register-context-menu-elevated.ps1"

function Get-MachineTrustedPeopleStore([bool] $ReadOnly) {
    $store = [System.Security.Cryptography.X509Certificates.X509Store]::new(
        "TrustedPeople",
        [System.Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine
    )
    $flags = if ($ReadOnly) {
        [System.Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly
    } else {
        [System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite
    }
    $store.Open($flags)
    return $store
}

function Test-InstallerOwnedCertificate {
    # NSIS may run 32-bit PowerShell; ownership is always stored in the native registry view.
    $view = if ([Environment]::Is64BitOperatingSystem) {
        [Microsoft.Win32.RegistryView]::Registry64
    } else {
        [Microsoft.Win32.RegistryView]::Registry32
    }
    $baseKey = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::LocalMachine,
        $view
    )
    try {
        $certificatesKey = $baseKey.OpenSubKey($certificateRegistrySubKey)
        if (-not $certificatesKey) {
            return $false
        }
        try {
            foreach ($thumbprint in $certificatesKey.GetSubKeyNames()) {
                $certificateKey = $certificatesKey.OpenSubKey($thumbprint)
                if (-not $certificateKey) {
                    continue
                }
                try {
                    if ($certificateKey.GetValue("Owned", 0) -eq 1) {
                        return $true
                    }
                } finally {
                    $certificateKey.Dispose()
                }
            }
        } finally {
            $certificatesKey.Dispose()
        }
    } finally {
        $baseKey.Dispose()
    }
    return $false
}

function Invoke-ElevatedCertificateAction([string] $ElevatedAction) {
    $powerShell = if (
        [Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess
    ) {
        # The elevated ShellExecute broker cannot resolve the Sysnative alias.
        Join-Path $env:WINDIR "SysWOW64\WindowsPowerShell\v1.0\powershell.exe"
    } else {
        Join-Path $env:WINDIR "System32\WindowsPowerShell\v1.0\powershell.exe"
    }
    $arguments = '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{0}" -Action {1}' `
        -f $elevatedScript, $ElevatedAction
    try {
        $process = Start-Process `
            -FilePath $powerShell `
            -ArgumentList $arguments `
            -Verb RunAs `
            -Wait `
            -PassThru
    } catch {
        $exception = $_.Exception
        while ($exception -and $exception.NativeErrorCode -ne 1223) {
            $exception = $exception.InnerException
        }
        if ($exception) {
            return $false
        }
        throw "Administrator approval is required to manage the PrivaSync MSIX signing certificate: $($_.Exception.Message)"
    }
    if ($process.ExitCode -ne 0) {
        $failureLog = Join-Path $installDir "register-context-menu-elevated.log"
        $detail = if (Test-Path -LiteralPath $failureLog) {
            (Get-Content -LiteralPath $failureLog -Raw).Trim()
        } else {
            "No detailed error was returned."
        }
        throw "Elevated certificate action '$ElevatedAction' failed with exit code $($process.ExitCode): $detail"
    }
    return $true
}

function Invoke-ShellRegistration([bool] $Unregister) {
    $regsvr32 = if (
        [Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess
    ) {
        Join-Path $env:WINDIR "Sysnative\regsvr32.exe"
    } else {
        Join-Path $env:WINDIR "System32\regsvr32.exe"
    }
    $arguments = if ($Unregister) {
        @("/u", "/s", "`"$shellDll`"")
    } else {
        @("/s", "`"$shellDll`"")
    }
    $process = Start-Process -FilePath $regsvr32 -ArgumentList $arguments -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "regsvr32 failed with exit code $($process.ExitCode)."
    }
}

$build = [int](Get-ItemProperty `
    "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion").CurrentBuildNumber

if ($Action -eq "PrepareInstall") {
    if ($build -ge 22000) {
        Get-AppxPackage -Name $packageName -ErrorAction SilentlyContinue |
            ForEach-Object {
                Remove-AppxPackage -Package $_.PackageFullName -ErrorAction Stop
            }
    }
    if (Test-Path -LiteralPath $shellDll -PathType Leaf) {
        Invoke-ShellRegistration $true
    }
    exit 0
}

if ($Action -eq "Install") {
    if ($build -lt 22000) {
        Invoke-ShellRegistration $false
        exit 0
    }

    if (Test-Path -LiteralPath $shellDll -PathType Leaf) {
        Invoke-ShellRegistration $true
    }
    $certificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new(
        $certificatePath
    )
    if ($certificate.Subject -ne "CN=PrivaSync") {
        throw "The sparse-package certificate publisher does not match CN=PrivaSync."
    }

    $signature = Get-AuthenticodeSignature -FilePath $packagePath
    if (
        -not $signature.SignerCertificate -or
        $signature.SignerCertificate.Thumbprint -ne $certificate.Thumbprint -or
        $signature.Status -in @("NotSigned", "HashMismatch")
    ) {
        throw "The sparse-package signature does not match its public certificate."
    }

    $store = Get-MachineTrustedPeopleStore $true
    try {
        $trusted = $store.Certificates.Find(
            [System.Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,
            $certificate.Thumbprint,
            $false
        )
    } finally {
        $store.Close()
    }
    if ($trusted.Count -eq 0) {
        if (-not (Invoke-ElevatedCertificateAction "InstallCertificate")) {
            Write-Warning "The PrivaSync app will be installed without the Windows 11 Explorer context menu because administrator approval was declined."
            exit 0
        }
    }

    try {
        Add-AppxPackage `
            -Path $packagePath `
            -ExternalLocation $installDir `
            -ForceUpdateFromAnyVersion
    } catch {
        $deploymentError = $_.Exception.Message
        try {
            if (Test-InstallerOwnedCertificate) {
                Invoke-ElevatedCertificateAction "RemoveCertificateIfUnused"
            }
        } catch {
            throw "Could not register the PrivaSync Explorer package: $deploymentError. Certificate cleanup also failed: $($_.Exception.Message)"
        }
        throw "Could not register the PrivaSync Explorer package: $deploymentError"
    }
    exit 0
}

if ($build -ge 22000) {
    Get-AppxPackage -Name $packageName -ErrorAction SilentlyContinue |
        ForEach-Object {
            Remove-AppxPackage -Package $_.PackageFullName -ErrorAction Stop
        }
    if (Test-Path -LiteralPath $shellDll -PathType Leaf) {
        Invoke-ShellRegistration $true
    }
    if (Test-InstallerOwnedCertificate) {
        if (-not (Invoke-ElevatedCertificateAction "RemoveCertificateIfUnused")) {
            Write-Warning "The PrivaSync Explorer signing certificate remains in the machine store because administrator approval was declined."
        }
    }
} elseif (Test-Path -LiteralPath $shellDll -PathType Leaf) {
    Invoke-ShellRegistration $true
}

$legacyMarkerPath = "HKCU:\Software\PrivaSync\ExplorerIdentity"
$legacyMarker = Get-ItemProperty -Path $legacyMarkerPath -ErrorAction SilentlyContinue
if ($legacyMarker.CertificateOwned -and $legacyMarker.CertificateThumbprint) {
    $legacyStore = [System.Security.Cryptography.X509Certificates.X509Store]::new(
        "TrustedPeople",
        [System.Security.Cryptography.X509Certificates.StoreLocation]::CurrentUser
    )
    try {
        $legacyStore.Open(
            [System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite
        )
        $legacyCertificates = $legacyStore.Certificates.Find(
            [System.Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,
            $legacyMarker.CertificateThumbprint,
            $false
        )
        foreach ($legacyCertificate in $legacyCertificates) {
            $legacyStore.Remove($legacyCertificate)
        }
    } finally {
        $legacyStore.Close()
    }
    Remove-Item -Path $legacyMarkerPath -Recurse -Force -ErrorAction Stop
}

exit 0
