[CmdletBinding(SupportsShouldProcess, ConfirmImpact='High')]
param(
  [Parameter(Mandatory=$true)][string]$AdapterName,
  [ValidateRange(0,4094)][int]$VlanId=0,
  [string]$ResultPath
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$result = @{ok=$false; needs_reboot=$false; message=''; address=''; interface='vEthernet (LANPrint-Printer)'; switch=''; vlan_id=$VlanId}
try {
  $admin = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  if (!$admin.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Administrator elevation is required.' }
  $edition = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').EditionID
  if ($edition -match '^Core') { throw 'Windows Home does not support Hyper-V. Windows 10 Pro/Enterprise/Education is required for a separate LAN adapter.' }
  $adapters = @(Get-NetAdapter | Where-Object Name -eq $AdapterName)
  if ($adapters.Count -ne 1 -or !$adapters[0].HardwareInterface) { throw 'Select one physical Ethernet uplink.' }
  $adapter = $adapters[0]
  if ($adapter.NdisPhysicalMedium -eq 9 -or $adapter.InterfaceDescription -match 'Wireless|Wi-Fi|802\.11') { throw 'This implementation requires Ethernet; Wi-Fi bridge is not validated.' }
  if (!$PSCmdlet.ShouldProcess($AdapterName, 'Enable Hyper-V if necessary and create a separate LANPrint virtual NIC on an external switch; uplink may be briefly interrupted')) { throw 'Cancelled.' }
  $feature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All
  if ($feature.State -ne 'Enabled') {
    Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All -All -NoRestart | Out-Null
    $result.needs_reboot = $true
    throw 'Hyper-V has been enabled. Restart Windows, then create the virtual adapter again.'
  }
  Import-Module Hyper-V
  $existingNics = @(Get-VMNetworkAdapter -ManagementOS | Where-Object Name -eq 'LANPrint-Printer')
  if ($existingNics.Count -gt 1) { throw 'Multiple LANPrint virtual NICs exist. Inspect Hyper-V configuration.' }
  if ($existingNics.Count -eq 1) {
    $attached = Get-VMSwitch -Name $existingNics[0].SwitchName
    if ($attached.SwitchType -ne 'External' -or $attached.NetAdapterInterfaceDescription -ne $adapter.InterfaceDescription) {
      throw 'Existing LANPrint virtual NIC uses another uplink. No switch or adapter was changed.'
    }
  }
  $switches = @(Get-VMSwitch | Where-Object { $_.SwitchType -eq 'External' -and $_.NetAdapterInterfaceDescription -eq $adapter.InterfaceDescription })
  if ($switches.Count -gt 1) { throw 'Multiple external switches match the uplink; inspect Hyper-V configuration.' }
  if ($switches.Count -eq 1) {
    $switch = $switches[0]
  } else {
    if (Get-VMSwitch -Name LANPrint -ErrorAction SilentlyContinue) { throw 'The existing LANPrint switch uses another uplink. No changes made to that switch.' }
    $switch = New-VMSwitch -Name LANPrint -NetAdapterName $adapter.Name -AllowManagementOS $true
  }
  $result.switch = $switch.Name
  $vnics = $existingNics
  if ($vnics.Count -gt 1) { throw 'Multiple LANPrint virtual NICs exist. Inspect Hyper-V configuration.' }
  $newNic = $vnics.Count -eq 0
  if (!$newNic -and $vnics[0].SwitchName -ne $switch.Name) { throw 'LANPrint virtual NIC is attached to another switch. No reassignment performed.' }
  if ($newNic) { Add-VMNetworkAdapter -ManagementOS -SwitchName $switch.Name -Name LANPrint-Printer }
  $vlan = Get-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName LANPrint-Printer
  $vlanChanged = ($VlanId -eq 0 -and $vlan.OperationMode -ne 'Untagged') -or
    ($VlanId -ne 0 -and ($vlan.OperationMode -ne 'Access' -or $vlan.AccessVlanId -ne $VlanId))
  if ($newNic -or $vlanChanged) {
    if ($VlanId -eq 0) { Set-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName LANPrint-Printer -Untagged }
    else { Set-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName LANPrint-Printer -Access -VlanId $VlanId }
  }
  $appliedVlan = Get-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName LANPrint-Printer
  if (($VlanId -eq 0 -and $appliedVlan.OperationMode -ne 'Untagged') -or
      ($VlanId -ne 0 -and ($appliedVlan.OperationMode -ne 'Access' -or $appliedVlan.AccessVlanId -ne $VlanId))) {
    throw 'Requested VLAN was not applied to the LANPrint switch port.'
  }
  $deadline = (Get-Date).AddSeconds(45)
  do {
    $nic = Get-NetAdapter -Name 'vEthernet (LANPrint-Printer)' -ErrorAction SilentlyContinue
    if ($nic) { break }
    Start-Sleep -Seconds 1
  } while ((Get-Date) -lt $deadline)
  if (!$nic -or $nic.HardwareInterface) { throw 'Dedicated Hyper-V adapter did not appear. Inspect Hyper-V before retrying.' }
  if ($newNic) {
    Set-NetIPInterface -InterfaceIndex $nic.ifIndex -AddressFamily IPv4 -Dhcp Enabled -InterfaceMetric 5000
    Set-DnsClient -InterfaceIndex $nic.ifIndex -RegisterThisConnectionsAddress $false
    Set-DnsClientServerAddress -InterfaceIndex $nic.ifIndex -ResetServerAddresses
  }
  if ($newNic -or $vlanChanged) {
    $ipif = Get-NetIPInterface -InterfaceIndex $nic.ifIndex -AddressFamily IPv4
    if ($ipif.Dhcp -eq 'Enabled') {
      # Clear a lease from the old/native VLAN and request one in the selected VLAN.
      & ipconfig.exe /release $nic.Name | Out-Null
      & ipconfig.exe /renew $nic.Name | Out-Null
    }
  }
  $deadline = (Get-Date).AddSeconds(45)
  do {
    $ip = Get-NetIPAddress -InterfaceIndex $nic.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
      Where-Object { $_.AddressState -eq 'Preferred' -and $_.IPAddress -notlike '169.254.*' -and $_.IPAddress -ne '0.0.0.0' } |
      Select-Object -First 1
    if ($ip) { break }
    Start-Sleep -Seconds 1
  } while ((Get-Date) -lt $deadline)
  $result.ok = $true
  if ($ip) { $result.address=$ip.IPAddress; $result.message='Dedicated virtual LAN adapter is ready.' }
  else { $result.message='Virtual adapter created, but no LAN IPv4 was received. Check DHCP or apply Static IP to this virtual adapter.' }
  if ($VlanId -ne 0) { $result.message += (' VLAN '+$VlanId+' uses tagged uplink traffic; the switch port must allow this VLAN.') }
  if ($vlanChanged -and !$newNic -and $ipif.Dhcp -ne 'Enabled') { $result.message += ' Existing Static IP was preserved: check that its subnet/gateway belong to the new VLAN.' }
} catch {
  $result.message = $_.Exception.Message
}
$json = ConvertTo-Json -InputObject $result -Compress
if ($ResultPath) { [IO.File]::WriteAllText($ResultPath, $json, [Text.UTF8Encoding]::new($false)) }
if (!$result.ok) { Write-Error -Message $result.message -ErrorAction Continue; exit 1 }
Write-Host $result.message
if ($result.address) { Write-Host ('LANPrint IPv4: '+$result.address) }
