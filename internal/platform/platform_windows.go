//go:build windows

package platform

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

func powershell(script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := utf16.Encode([]rune("[Console]::OutputEncoding = [Text.UTF8Encoding]::new(); $ErrorActionPreference='Stop'; " + script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, e := cmd.CombinedOutput()
	if e != nil {
		return nil, fmt.Errorf("PowerShell: %w: %s", e, out)
	}
	return out, nil
}
func Printers() ([]Printer, error) {
	b, e := powershell(`$items=@(Get-Printer | ForEach-Object { @{name=$_.Name;driver=$_.DriverName;port=$_.PortName;status=[string]$_.PrinterStatus} }); ConvertTo-Json -InputObject $items -Compress`)
	if e != nil {
		return nil, e
	}
	var p []Printer
	e = json.Unmarshal(b, &p)
	return p, e
}
func Adapters() ([]Adapter, error) {
	b, e := powershell(`$items=@(Get-NetAdapter | ForEach-Object { $a=$_; $i=Get-NetIPInterface -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue; $gw=(Get-NetRoute -InterfaceIndex $a.ifIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Select-Object -First 1).NextHop; @(Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue) | ForEach-Object { @{name=$a.Name;description=$a.InterfaceDescription;mac=$a.MacAddress;address=$_.IPAddress;mask=[string]$_.PrefixLength;gateway=[string]$gw;up=($a.Status -eq 'Up');dhcp=($i.Dhcp -eq 'Enabled')} } }); ConvertTo-Json -InputObject $items -Compress`)
	if e != nil {
		return nil, e
	}
	var a []Adapter
	e = json.Unmarshal(b, &a)
	for i := range a {
		var p int
		fmt.Sscan(a[i].Mask, &p)
		if p >= 0 && p <= 32 {
			a[i].Mask = net.IP(net.CIDRMask(p, 32)).String()
		}
	}
	return a, e
}
func OSJobs(printer string) (string, error) {
	payload, _ := json.Marshal(printer)
	b, e := powershell(`$p=ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(payload) + `'))); Get-PrintJob -PrinterName $p | Select-Object ID,DocumentName,JobStatus,SubmittedTime | Format-Table -AutoSize | Out-String`)
	return string(b), e
}

var shell = syscall.NewLazyDLL("shell32.dll")

func OpenURL(url string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	u, e := syscall.UTF16PtrFromString(url)
	if e != nil {
		return e
	}
	r, _, _ := shell.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(u)), 0, 0, 1)
	if r <= 32 {
		return fmt.Errorf("ShellExecute: %d", r)
	}
	return nil
}
func AutoRun(exe string, enabled bool) error {
	payload, _ := json.Marshal(exe)
	script := `$key='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'; `
	if enabled {
		script += `$p=ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(payload) + `'))); New-ItemProperty -Path $key -Name LANPrint -Value ('"'+$p+'" --autostart') -PropertyType String -Force | Out-Null`
	} else {
		script += `Remove-ItemProperty -Path $key -Name LANPrint -ErrorAction SilentlyContinue`
	}
	_, e := powershell(script)
	return e
}
func ApplyNetwork(adapter, mode, ip, mask, gateway, dns string) error {
	payload, _ := json.Marshal(map[string]string{"adapter": adapter, "mode": mode, "ip": ip, "mask": mask, "gateway": gateway, "dns": dns})
	script := `$ErrorActionPreference='Stop'; $p=ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(payload) + `'))); $a=Get-NetAdapter -Name $p.adapter; if($p.mode -eq 'dhcp'){ Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 | Where-Object PrefixOrigin -eq 'Manual' | Remove-NetIPAddress -Confirm:$false; Get-NetRoute -InterfaceIndex $a.ifIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false; Set-NetIPInterface -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -Dhcp Enabled; Set-DnsClientServerAddress -InterfaceIndex $a.ifIndex -ResetServerAddresses; ipconfig /renew $a.Name } else { $mask=[Net.IPAddress]::Parse($p.mask).GetAddressBytes(); $bits=($mask | ForEach-Object {[Convert]::ToString($_,2).PadLeft(8,'0')}) -join ''; if($bits -notmatch '^1*0*$'){throw 'Invalid mask'}; $prefix=($bits -replace '0','').Length; Set-NetIPInterface -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -Dhcp Disabled; Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 | Remove-NetIPAddress -Confirm:$false; Get-NetRoute -InterfaceIndex $a.ifIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false; $params=@{InterfaceIndex=$a.ifIndex;IPAddress=$p.ip;PrefixLength=$prefix}; if($p.gateway){$params.DefaultGateway=$p.gateway}; New-NetIPAddress @params | Out-Null; if($p.dns){Set-DnsClientServerAddress -InterfaceIndex $a.ifIndex -ServerAddresses ($p.dns -split ',')} }; Read-Host 'Готово. Нажмите Enter'`
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	args, _ := syscall.UTF16PtrFromString("-NoLogo -NoProfile -EncodedCommand " + base64.StdEncoding.EncodeToString(b))
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString("powershell.exe")
	r, _, _ := shell.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(args)), 0, 1)
	if r <= 32 {
		return fmt.Errorf("elevation: %d", r)
	}
	return nil
}
