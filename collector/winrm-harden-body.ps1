# ---------------------------------------------------------------------------
# Body of the generated gatus-winrm-setup.ps1. setup-winrm.sh prepends the
# embedded certificates and this machine's details, so there are no parameters
# to pass and nothing to look up.
#
# What it does, in order:
#   1  a local account that the client certificate maps to, with every logon
#      type denied except the network logon certificate auth needs, and
#      membership of no group at all
#   2  trusts the collector's CA
#   3  a JEA endpoint exposing ONE read-only function, in NoLanguage mode,
#      running privileged work under a virtual account
#   4  an HTTPS listener using this host's embedded certificate
#   5  certificate auth on, Basic off, unencrypted off, plaintext listener gone
#   6  firewall: 5986 from the collector's address only
#
# Idempotent. Safe to re-run.
# ---------------------------------------------------------------------------
Set-StrictMode -Version Latest

function Write-Step($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Note($m) { Write-Host "    $m" -ForegroundColor DarkGray }
function Write-Bad($m)  { Write-Host "    $m" -ForegroundColor Red }

if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
        ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this in an ADMIN PowerShell. Right click PowerShell, Run as administrator.'
}

$me = $env:COMPUTERNAME.ToUpper()
if (-not $Pfx.ContainsKey($me)) {
    Write-Bad "This host is '$me' and there is no certificate for it in this script."
    Write-Bad "Known hosts: $(($Pfx.Keys | Sort-Object) -join ', ')"
    throw "Add $me to HOSTS in setup-winrm.sh, re-run it, and copy the new script here."
}
Write-Host ""
Write-Host "Setting up WinRM on $me for the Gatus collector at $CollectorIp" -ForegroundColor White
Write-Host ""

# [System.IO.Path]::GetTempPath() rather than $env:TEMP: TEMP can hold an 8.3
# short path, which some PowerShell providers refuse to resolve, and the
# cleanup below must not be the thing that fails. The PFX lands here.
$work = Join-Path ([System.IO.Path]::GetTempPath()) "gatus-winrm-$([guid]::NewGuid().ToString('N'))"
[void][System.IO.Directory]::CreateDirectory($work)
try {
    $caPath  = Join-Path $work 'ca.crt'
    $pfxPath = Join-Path $work 'host.pfx'
    [IO.File]::WriteAllBytes($caPath,  [Convert]::FromBase64String($CaB64))
    [IO.File]::WriteAllBytes($pfxPath, [Convert]::FromBase64String($Pfx[$me].Data))
    $pfxPw = ConvertTo-SecureString $Pfx[$me].Pass -AsPlainText -Force
    $upn = "$AccountName@localhost"

    # --- 1. the mapped local account ---------------------------------------
    Write-Step "Local account $AccountName"
    $bytes = [byte[]]::new(48)
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $pw = ConvertTo-SecureString ([Convert]::ToBase64String($bytes) + '!aA9') -AsPlainText -Force
    if (Get-LocalUser -Name $AccountName -ErrorAction SilentlyContinue) {
        Set-LocalUser -Name $AccountName -Password $pw -PasswordNeverExpires $true
        Write-Note 'existed, password rotated'
    } else {
        New-LocalUser -Name $AccountName -Password $pw -PasswordNeverExpires:$true `
            -Description 'Gatus read-only inventory (cert + JEA)' | Out-Null
        Write-Note 'created'
    }
    # The password is random and discarded on purpose. Nothing authenticates
    # with it; the certificate is the credential.
    foreach ($g in @('Administrators', 'Remote Management Users', 'Hyper-V Administrators')) {
        if (Get-LocalGroupMember -Group $g -Member $AccountName -ErrorAction SilentlyContinue) {
            Remove-LocalGroupMember -Group $g -Member $AccountName -Confirm:$false
            Write-Note "removed from $g"
        }
    }

    # Deny every logon type except the network logon that certificate mapping
    # needs. SeDenyNetworkLogonRight is deliberately NOT set: cert auth is a
    # network logon and denying it breaks the whole thing.
    #
    # This is defence in depth, not the primary control. The account is already
    # in no group and its password is random and discarded; the firewall, the
    # client certificate and the JEA endpoint are what actually protect the
    # host. So a failure here WARNS and carries on rather than aborting the
    # whole setup, and says exactly what to do by hand.
    #
    # Two things make this fiddly, both learned the hard way:
    #   - an exported template may have no [Privilege Rights] section at all, or
    #     not list a given right. Appending the line to the end of the file puts
    #     it outside the section, where secedit silently ignores it.
    #   - secedit /configure REPLACES the rights named in the template, so the
    #     existing holders of each right have to be carried over or they are
    #     revoked. Writing a bare template would quietly undo someone else's
    #     deny entries.
    # So: export, read the current holders, write a minimal template with the
    # merged lists, apply, then verify.
    $sid = (Get-LocalUser -Name $AccountName).SID.Value
    $rights = @('SeDenyInteractiveLogonRight', 'SeDenyRemoteInteractiveLogonRight',
                'SeDenyBatchLogonRight', 'SeDenyServiceLogonRight')
    $rightsApplied = $false
    try {
        $exp = Join-Path $work 'current.inf'
        $null = secedit /export /areas USER_RIGHTS /cfg $exp 2>&1
        if (-not (Test-Path $exp)) { throw 'secedit /export produced no file' }

        # Read the existing holders of each right, from inside the section only.
        $current = @{}
        $inSection = $false
        foreach ($line in (Get-Content $exp)) {
            if ($line -match '^\s*\[') { $inSection = ($line -match '^\s*\[Privilege Rights\]') ; continue }
            if (-not $inSection) { continue }
            if ($line -match '^\s*([A-Za-z]+)\s*=\s*(.*)$') {
                $current[$Matches[1]] = $Matches[2].Trim()
            }
        }

        $body = @('[Unicode]', 'Unicode=yes', '[Version]',
                  'signature="$CHICAGO$"', 'Revision=1', '[Privilege Rights]')
        foreach ($r in $rights) {
            $holders = @()
            if ($current.ContainsKey($r) -and $current[$r]) {
                $holders = @($current[$r].Split(',') | ForEach-Object { $_.Trim() } |
                             Where-Object { $_ })
            }
            if ($holders -notcontains "*$sid") { $holders += "*$sid" }
            $body += "$r = $($holders -join ',')"
        }

        $tpl = Join-Path $work 'deny.inf'
        $sdb = Join-Path $work 'deny.sdb'
        Set-Content -Path $tpl -Value $body -Encoding Unicode
        $out = secedit /configure /db $sdb /cfg $tpl /areas USER_RIGHTS /quiet 2>&1

        # Verify. secedit reports success in situations where nothing changed.
        $chk = Join-Path $work 'verify.inf'
        Remove-Item $chk -Force -ErrorAction SilentlyContinue
        $null = secedit /export /areas USER_RIGHTS /cfg $chk 2>&1
        $missing = @()
        if (Test-Path $chk) {
            $vIn = $false
            $after = @{}
            foreach ($line in (Get-Content $chk)) {
                if ($line -match '^\s*\[') { $vIn = ($line -match '^\s*\[Privilege Rights\]'); continue }
                if (-not $vIn) { continue }
                if ($line -match '^\s*([A-Za-z]+)\s*=\s*(.*)$') { $after[$Matches[1]] = $Matches[2] }
            }
            foreach ($r in $rights) {
                if (-not ($after.ContainsKey($r) -and $after[$r] -like "*$sid*")) { $missing += $r }
            }
        } else {
            $missing = $rights
        }

        if ($missing.Count -eq 0) {
            $rightsApplied = $true
            Write-Note 'denied interactive, RDP, batch and service logon (verified)'
        } else {
            throw ("not applied for: " + ($missing -join ', '))
        }
    } catch {
        Write-Host ""
        Write-Host "    WARNING: could not deny logon rights for $AccountName." -ForegroundColor Yellow
        Write-Host "    Reason: $($_.Exception.Message)" -ForegroundColor Yellow
        Write-Host "    This is a secondary control and setup is continuing. The account is" -ForegroundColor Yellow
        Write-Host "    still in no group, its password is random and discarded, and WinRM is" -ForegroundColor Yellow
        Write-Host "    still restricted to one address, one certificate and one command." -ForegroundColor Yellow
        Write-Host "    To add it by hand (or in Group Policy if that manages user rights here):" -ForegroundColor Yellow
        Write-Host "      secpol.msc > Local Policies > User Rights Assignment" -ForegroundColor Yellow
        Write-Host "      add $env:COMPUTERNAME\$AccountName to:" -ForegroundColor Yellow
        Write-Host "        Deny log on locally" -ForegroundColor Yellow
        Write-Host "        Deny log on through Remote Desktop Services" -ForegroundColor Yellow
        Write-Host "        Deny log on as a batch job" -ForegroundColor Yellow
        Write-Host "        Deny log on as a service" -ForegroundColor Yellow
        Write-Host "      Do NOT add 'Deny access to this computer from the network':" -ForegroundColor Yellow
        Write-Host "      certificate auth is a network logon and that would break it." -ForegroundColor Yellow
        Write-Host ""
    }

    # --- 2. trust the CA ---------------------------------------------------
    Write-Step 'Trust the collector CA'
    $ca = Import-Certificate -FilePath $caPath -CertStoreLocation Cert:\LocalMachine\Root
    if ($ca.Thumbprint -ne $CaThumbprint) {
        throw "Embedded CA thumbprint $($ca.Thumbprint) does not match $CaThumbprint"
    }
    Write-Note $ca.Thumbprint

    # --- 3. JEA endpoint ---------------------------------------------------
    Write-Step "JEA endpoint $ConfigName"
    $moduleDir = "$env:ProgramFiles\WindowsPowerShell\Modules\GatusInventory"
    $roleDir   = "$env:ProgramData\GatusInventory"
    New-Item -ItemType Directory -Force -Path "$moduleDir\RoleCapabilities" | Out-Null
    New-Item -ItemType Directory -Force -Path "$roleDir\Transcripts" | Out-Null

    $inventory = @'
function Get-GatusInventory {
    [CmdletBinding()]
    param()
    $ErrorActionPreference = 'Stop'
    $out = [ordered]@{}
    $os   = Get-CimInstance Win32_OperatingSystem
    $cs   = Get-CimInstance Win32_ComputerSystem
    $bios = Get-CimInstance Win32_BIOS
    $cpus = @(Get-CimInstance Win32_Processor)
    $loads = @($cpus | Where-Object { $null -ne $_.LoadPercentage } | ForEach-Object { [double]$_.LoadPercentage })
    $tk = [double]$os.TotalVisibleMemorySize
    $fk = [double]$os.FreePhysicalMemory
    $out.os = [ordered]@{
        caption = $os.Caption; version = $os.Version; build = $os.BuildNumber
        installedOn = if ($os.InstallDate) { $os.InstallDate.ToString('o') } else { $null }
        lastBoot = if ($os.LastBootUpTime) { $os.LastBootUpTime.ToString('o') } else { $null }
        uptimeHours = if ($os.LastBootUpTime) { [math]::Round(((Get-Date) - $os.LastBootUpTime).TotalHours, 2) } else { $null }
    }
    $out.host = [ordered]@{
        name = $cs.Name; domain = $cs.Domain; manufacturer = $cs.Manufacturer
        model = $cs.Model; serial = $bios.SerialNumber; biosVersion = $bios.SMBIOSBIOSVersion
    }
    $out.cpu = [ordered]@{
        name = $cpus[0].Name; sockets = $cpus.Count
        coresTotal = ($cpus | Measure-Object -Property NumberOfCores -Sum).Sum
        logicalTotal = ($cpus | Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum
        maxClockMHz = $cpus[0].MaxClockSpeed
        loadPercent = if ($loads.Count) { [math]::Round(($loads | Measure-Object -Average).Average, 1) } else { $null }
    }
    $out.memory = [ordered]@{
        totalGB = [math]::Round($tk / 1048576, 2)
        freeGB  = [math]::Round($fk / 1048576, 2)
        usedGB  = [math]::Round(($tk - $fk) / 1048576, 2)
        usedPct = if ($tk -gt 0) { [math]::Round((($tk - $fk) / $tk) * 100, 1) } else { $null }
    }
    $out.volumes = @(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object {
        $sz = [double]$_.Size; $fr = [double]$_.FreeSpace
        [ordered]@{
            drive = $_.DeviceID; label = $_.VolumeName; fs = $_.FileSystem
            totalGB = [math]::Round($sz / 1073741824, 2)
            freeGB  = [math]::Round($fr / 1073741824, 2)
            usedPct = if ($sz -gt 0) { [math]::Round((($sz - $fr) / $sz) * 100, 1) } else { $null }
        }
    })
    $out.adapters = @()
    try {
        $out.adapters = @(Get-NetAdapter -ErrorAction Stop | Where-Object { $_.Status -ne 'Not Present' } |
            Sort-Object Name | ForEach-Object {
                [ordered]@{ name = $_.Name; status = [string]$_.Status; linkSpeed = [string]$_.LinkSpeed; mac = $_.MacAddress }
            })
    } catch {}
    $out.errors24h = $null
    try {
        $since = (Get-Date).AddHours(-24)
        $out.errors24h = @(Get-WinEvent -FilterHashtable @{LogName='System'; Level=1,2; StartTime=$since} -MaxEvents 200 -ErrorAction Stop).Count
    } catch {}
    $out.hyperv = [ordered]@{ present = $false }
    $out.vms = @()
    try {
        $all = @(Get-VM)
        $out.hyperv.present = $true
        try {
            $h = Get-VMHost
            $out.hyperv.virtualHardDiskPath = $h.VirtualHardDiskPath
            $out.hyperv.virtualMachinePath  = $h.VirtualMachinePath
            $out.hyperv.logicalProcessors   = $h.LogicalProcessorCount
            $out.hyperv.memoryCapacityGB    = [math]::Round($h.MemoryCapacity / 1073741824, 2)
        } catch {}
        $out.vms = @($all | Sort-Object Name | ForEach-Object {
            [ordered]@{
                name = $_.Name; state = [string]$_.State; status = [string]$_.Status
                cpuPercent = $_.CPUUsage
                memoryGB = if ($_.MemoryAssigned) { [math]::Round($_.MemoryAssigned / 1073741824, 2) } else { 0 }
                memoryDemandGB = if ($_.MemoryDemand) { [math]::Round($_.MemoryDemand / 1073741824, 2) } else { $null }
                vcpu = $_.ProcessorCount
                uptimeHours = if ($_.Uptime) { [math]::Round($_.Uptime.TotalHours, 2) } else { 0 }
                generation = $_.Generation
                heartbeat = [string]$_.Heartbeat
                replication = [string]$_.ReplicationHealth
            }
        })
    } catch { $out.hyperv.error = $_.Exception.Message }
    $out.cluster = $null
    try { $out.cluster = [ordered]@{ name = (Get-Cluster -ErrorAction Stop).Name } } catch {}
    $out | ConvertTo-Json -Depth 6 -Compress
}
'@
    Set-Content -Path "$moduleDir\GatusInventory.psm1" -Value $inventory -Encoding UTF8
    New-ModuleManifest -Path "$moduleDir\GatusInventory.psd1" `
        -RootModule 'GatusInventory.psm1' -FunctionsToExport 'Get-GatusInventory'
    New-PSRoleCapabilityFile -Path "$moduleDir\RoleCapabilities\GatusInventory.psrc" `
        -VisibleFunctions 'Get-GatusInventory' -ModulesToImport 'GatusInventory' -Force

    $pssc = Join-Path $work 'config.pssc'
    New-PSSessionConfigurationFile -Path $pssc `
        -SessionType RestrictedRemoteServer -RunAsVirtualAccount -LanguageMode NoLanguage `
        -TranscriptDirectory "$roleDir\Transcripts" `
        -RoleDefinitions @{ "$env:COMPUTERNAME\$AccountName" = @{ RoleCapabilities = 'GatusInventory' } }
    if (Get-PSSessionConfiguration -Name $ConfigName -ErrorAction SilentlyContinue) {
        Unregister-PSSessionConfiguration -Name $ConfigName -Force
    }
    Register-PSSessionConfiguration -Name $ConfigName -Path $pssc -Force | Out-Null
    Write-Note 'one visible function, NoLanguage, virtual account, transcripts on'

    # --- 4. HTTPS listener -------------------------------------------------
    Write-Step 'HTTPS listener on 5986'
    $serverCert = Import-PfxCertificate -FilePath $pfxPath `
        -CertStoreLocation Cert:\LocalMachine\My -Password $pfxPw
    Get-ChildItem WSMan:\localhost\Listener | Where-Object { $_.Keys -contains 'Transport=HTTPS' } |
        ForEach-Object { Remove-Item "WSMan:\localhost\Listener\$($_.Name)" -Recurse -Force }
    New-Item -Path WSMan:\localhost\Listener -Transport HTTPS -Address * `
        -CertificateThumbPrint $serverCert.Thumbprint -Force | Out-Null
    Write-Note "CA-signed certificate $($serverCert.Thumbprint), so the collector can verify this host"

    # --- 5. auth surface ---------------------------------------------------
    Write-Step 'Authentication'
    Set-Item WSMan:\localhost\Service\Auth\Certificate -Value $true
    Set-Item WSMan:\localhost\Service\Auth\Basic       -Value $false
    Set-Item WSMan:\localhost\Service\AllowUnencrypted -Value $false
    Write-Note 'certificate auth on, Basic off, unencrypted off'

    # Scoped to the JEA endpoint URI, so this certificate cannot open a shell
    # or run winrs even if it is stolen.
    $jeaUri = "http://schemas.microsoft.com/powershell/$ConfigName"
    Get-ChildItem WSMan:\localhost\ClientCertificate -ErrorAction SilentlyContinue |
        ForEach-Object {
            $s = (Get-Item "WSMan:\localhost\ClientCertificate\$($_.Name)\Subject" -ErrorAction SilentlyContinue).Value
            if ($s -eq $upn) { Remove-Item "WSMan:\localhost\ClientCertificate\$($_.Name)" -Recurse -Force }
        }
    $cred = New-Object System.Management.Automation.PSCredential("$env:COMPUTERNAME\$AccountName", $pw)
    New-Item -Path WSMan:\localhost\ClientCertificate `
        -Subject $upn -URI $jeaUri -Issuer $CaThumbprint -Credential $cred -Force | Out-Null
    Write-Note "certificate mapped to $AccountName, usable only for $ConfigName"

    Write-Step 'Close the plaintext path'
    Get-ChildItem WSMan:\localhost\Listener | Where-Object { $_.Keys -contains 'Transport=HTTP' } |
        ForEach-Object {
            Remove-Item "WSMan:\localhost\Listener\$($_.Name)" -Recurse -Force
            Write-Note 'removed the HTTP listener'
        }
    Get-NetFirewallRule -ErrorAction SilentlyContinue |
        Where-Object { $_.DisplayName -like 'Windows Remote Management*' -and $_.Enabled -eq 'True' } |
        ForEach-Object { Disable-NetFirewallRule -Name $_.Name; Write-Note "disabled built-in rule: $($_.DisplayName)" }

    # --- 6. firewall -------------------------------------------------------
    Write-Step 'Firewall'
    $ruleName = 'Gatus WinRM (HTTPS, collector only)'
    Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Action Allow `
        -Protocol TCP -LocalPort 5986 -RemoteAddress $CollectorIp -Profile Any | Out-Null
    Write-Note "5986 inbound from $CollectorIp only"

    Restart-Service WinRM

    # --- prove it works locally before declaring success -------------------
    Write-Step 'Self test'
    $probe = Get-PSSessionConfiguration -Name $ConfigName -ErrorAction SilentlyContinue
    if (-not $probe) { throw 'JEA endpoint did not register' }
    $fw = Get-NetFirewallRule -DisplayName $ruleName | Get-NetFirewallAddressFilter
    $listener = Get-ChildItem WSMan:\localhost\Listener | Where-Object { $_.Keys -contains 'Transport=HTTPS' }
    if (-not $listener) { throw 'HTTPS listener missing' }
    $inv = & (Get-Module GatusInventory -ListAvailable | Select-Object -First 1 |
              ForEach-Object { Import-Module $_.Path -Force -PassThru }) { Get-GatusInventory } 2>$null
    Write-Note "JEA endpoint   : $($probe.Name)"
    Write-Note "allowed from   : $($fw.RemoteAddress)"
    Write-Note "HTTPS listener : present"

    Write-Host ''
    Write-Host "$me is ready." -ForegroundColor Green
    if (-not $rightsApplied) {
        Write-Host '  NOTE: logon-right denial was not applied, see the warning above.' -ForegroundColor Yellow
    }
    Write-Host '  Delete this script from the host now.'
    Write-Host '  On the Gatus box: docker compose restart hv-collector'
    Write-Host ''
}
finally {
    # The embedded PFX and the exported policy must not be left on disk. Use the
    # .NET call, which copes with short paths, and say so if it ever fails
    # rather than leaving a private key lying around silently.
    try {
        if ([System.IO.Directory]::Exists($work)) {
            [System.IO.Directory]::Delete($work, $true)
        }
    } catch {
        Write-Host "    WARNING: could not delete $work - it holds this host's" -ForegroundColor Yellow
        Write-Host "    certificate and private key. Delete it by hand." -ForegroundColor Yellow
    }
}
