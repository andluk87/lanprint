#Requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess, ConfirmImpact='High')]
param(
 [Parameter(Mandatory=$true)][string]$Address,
 [Parameter(Mandatory=$true)][string]$InterfaceAlias,
 [string]$Exe = (Join-Path (Split-Path $PSScriptRoot) 'LANPrint.exe')
)
$ErrorActionPreference = 'Stop'
$Exe = (Resolve-Path $Exe).Path
if (!(Get-NetIPAddress -InterfaceAlias $InterfaceAlias -AddressFamily IPv4 | Where-Object IPAddress -eq $Address)) { throw 'Address is not assigned to selected adapter' }
if (!$PSCmdlet.ShouldProcess($InterfaceAlias, 'Allow LANPrint TCP 9100/8631/8765 and UDP 5353 from LocalSubnet on Private profile')) { return }
Get-NetFirewallRule -DisplayName 'LANPrint TCP','LANPrint mDNS' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName 'LANPrint TCP' -Program $Exe -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9100,8631,8765 -LocalAddress $Address -InterfaceAlias $InterfaceAlias -RemoteAddress LocalSubnet -Profile Private | Out-Null
New-NetFirewallRule -DisplayName 'LANPrint mDNS' -Program $Exe -Direction Inbound -Action Allow -Protocol UDP -LocalPort 5353 -InterfaceAlias $InterfaceAlias -RemoteAddress LocalSubnet -Profile Private | Out-Null
Write-Host 'After a DHCP address change, update the address-specific TCP rule. Custom ports require custom rules.'
