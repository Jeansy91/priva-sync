param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("InstallCertificate", "RemoveCertificateIfUnused")]
    [string] $Action
)

$ErrorActionPreference = "Stop"
$packageName = "PrivaSync.Shell"
$certificateRegistrySubKey = "Software\PrivaSync\ExplorerIdentity\Certificates"
$installDir = [System.IO.Path]::GetFullPath($PSScriptRoot)
$packagePath = Join-Path $installDir "privasync-identity.msix"
$certificatePath = Join-Path $installDir "privasync-package.cer"
$failureLog = Join-Path $installDir "register-context-menu-elevated.log"
$registryView = if ([Environment]::Is64BitOperatingSystem) {
    [Microsoft.Win32.RegistryView]::Registry64
} else {
    [Microsoft.Win32.RegistryView]::Registry32
}

trap {
    ($_ | Out-String) | Set-Content -LiteralPath $failureLog -Encoding UTF8
    exit 1
}
if (Test-Path -LiteralPath $failureLog) {
    Remove-Item -LiteralPath $failureLog -Force
}

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "The certificate helper must run with administrator privileges."
}

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

if ($Action -eq "InstallCertificate") {
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

    $store = Get-MachineTrustedPeopleStore $false
    try {
        $trusted = $store.Certificates.Find(
            [System.Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,
            $certificate.Thumbprint,
            $false
        )
        if ($trusted.Count -gt 0) {
            exit 0
        }

        $store.Add($certificate)
        $baseKey = $null
        try {
            $baseKey = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
                [Microsoft.Win32.RegistryHive]::LocalMachine,
                $registryView
            )
            $ownershipKey = $baseKey.CreateSubKey(
                "$certificateRegistrySubKey\$($certificate.Thumbprint)"
            )
            try {
                $ownershipKey.SetValue(
                    "Owned",
                    1,
                    [Microsoft.Win32.RegistryValueKind]::DWord
                )
            } finally {
                $ownershipKey.Dispose()
            }
        } catch {
            $store.Remove($certificate)
            throw
        } finally {
            if ($baseKey) {
                $baseKey.Dispose()
            }
        }
    } finally {
        $store.Close()
    }
    exit 0
}

$packages = @(Get-AppxPackage -AllUsers -Name $packageName)
if ($packages.Count -gt 0) {
    exit 0
}

$baseKey = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
    [Microsoft.Win32.RegistryHive]::LocalMachine,
    $registryView
)
try {
    $certificatesKey = $baseKey.OpenSubKey($certificateRegistrySubKey, $true)
    if (-not $certificatesKey) {
        exit 0
    }

    $store = $null
    try {
        $store = Get-MachineTrustedPeopleStore $false
        $thumbprints = @($certificatesKey.GetSubKeyNames())
        foreach ($thumbprint in $thumbprints) {
            $ownershipKey = $certificatesKey.OpenSubKey($thumbprint)
            $owned = $false
            if ($ownershipKey) {
                try {
                    $owned = $ownershipKey.GetValue("Owned", 0) -eq 1
                } finally {
                    $ownershipKey.Dispose()
                }
            }
            if ($owned) {
                $certificates = $store.Certificates.Find(
                    [System.Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,
                    $thumbprint,
                    $false
                )
                foreach ($certificate in $certificates) {
                    $store.Remove($certificate)
                }
            }
            $certificatesKey.DeleteSubKeyTree($thumbprint)
        }
    } finally {
        if ($store) {
            $store.Close()
        }
        $certificatesKey.Dispose()
    }
    $baseKey.DeleteSubKeyTree($certificateRegistrySubKey)
} finally {
    $baseKey.Dispose()
}

exit 0
