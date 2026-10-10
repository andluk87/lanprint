[CmdletBinding()]
param([string]$Config = (Join-Path $env:LOCALAPPDATA 'LANPrint\config.json'))
$ErrorActionPreference = 'Stop'
$c = Get-Content -Raw $Config | ConvertFrom-Json
Get-Printer -Name $c.printer | Format-List Name,DriverName,PortName,PrinterStatus
Get-NetAdapter -Name $c.interface | Format-List Name,MacAddress,Status
$ip = Get-NetIPAddress -InterfaceAlias $c.interface -IPAddress $c.address -AddressFamily IPv4
if ($ip.AddressState -ne 'Preferred') { throw "Address state: $($ip.AddressState)" }
foreach ($port in @($c.raw_port, $c.ipp_port, $c.admin_port)) {
  if (!(Test-NetConnection -ComputerName $c.address -Port $port -InformationLevel Quiet)) { throw "Cannot reach TCP $port" }
}
$url = "http://$($c.address):$($c.admin_port)/api/status"
$status = Invoke-RestMethod -Uri $url -Headers @{Authorization="Bearer $($c.admin_token)"}
$status | Select-Object name,address,printer,status,error | Format-List
if ($status.error) { throw $status.error }
Write-Host 'Host reachability/status checked. This is NOT a print/Bridge acceptance test: test from another LAN host and inspect actual printed sheets.'
