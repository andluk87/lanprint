#Requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess, ConfirmImpact='High')]
param([Parameter(Mandatory=$true)][string]$AdapterName,[ValidateRange(0,4094)][int]$VlanId=0)
# Optional developer entry point. The EXE embeds the same canonical script;
# end users create the adapter directly from the LANPrint GUI.
$ErrorActionPreference = 'Stop'
$helper = Join-Path (Split-Path $PSScriptRoot) 'internal\platform\bridge.ps1'
if (!(Test-Path $helper)) { throw 'Source checkout required for this optional script. Use Create adapter in LANPrint.exe instead.' }
& $helper -AdapterName $AdapterName -VlanId $VlanId -Confirm:$true
