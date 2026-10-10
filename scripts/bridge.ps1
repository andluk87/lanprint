#Requires -RunAsAdministrator
#Requires -Modules Hyper-V
[CmdletBinding(SupportsShouldProcess, ConfirmImpact='High')]
param([Parameter(Mandatory=$true)][string]$AdapterName)
$ErrorActionPreference = 'Stop'
$adapter = Get-NetAdapter -Name $AdapterName
if ($adapter.NdisPhysicalMedium -eq 9 -or $adapter.InterfaceDescription -match 'Wireless|Wi-Fi|802\.11') {
  throw 'Use Ethernet for the external Hyper-V switch. Wi-Fi bridge is not validated.'
}
if (Get-VMSwitch -Name 'LANPrint' -ErrorAction SilentlyContinue) {
  throw 'LANPrint switch already exists; inspect it before making changes.'
}
if (!$PSCmdlet.ShouldProcess($AdapterName, 'Create external switch and separate ManagementOS NIC LANPrint-Printer; briefly interrupts this adapter')) { return }
New-VMSwitch -Name 'LANPrint' -NetAdapterName $AdapterName -AllowManagementOS $true | Out-Null
Add-VMNetworkAdapter -ManagementOS -SwitchName 'LANPrint' -Name 'LANPrint-Printer'
$nic = Get-NetAdapter -Name 'vEthernet (LANPrint-Printer)'
Set-NetIPInterface -InterfaceIndex $nic.ifIndex -AddressFamily IPv4 -Dhcp Enabled -InterfaceMetric 5000
Set-DnsClient -InterfaceIndex $nic.ifIndex -RegisterThisConnectionsAddress $false
Set-DnsClientServerAddress -InterfaceIndex $nic.ifIndex -ResetServerAddresses
Write-Host 'Select vEthernet (LANPrint-Printer) in LANPrint. Wait for DHCP or explicitly apply Static IP in the GUI.'
Write-Host 'Verify a separate address/MAC and actual printing from another LAN machine. Hardware validation is required.'
