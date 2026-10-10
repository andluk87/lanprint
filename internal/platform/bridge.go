package platform

import (
 _ "embed"
 "encoding/base64"
 "encoding/json"
 "fmt"
)

const VirtualAdapter = "vEthernet (LANPrint-Printer)"

//go:embed bridge.ps1
var bridgeScript string

type BridgeResult struct {
 OK bool `json:"ok"`
 NeedsReboot bool `json:"needs_reboot"`
 Message string `json:"message"`
 Address string `json:"address"`
 Interface string `json:"interface"`
 Switch string `json:"switch"`
 VLANID int `json:"vlan_id"`
}

func bridgeCommand(uplink,resultPath string,vlan int) string {
 data,_:=json.Marshal(map[string]any{"adapter":uplink,"result":resultPath,"vlan":vlan})
 encoded:=base64.StdEncoding.EncodeToString(data)
 return `$ErrorActionPreference='Stop'; $p=ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('`+encoded+`'))); & {`+"\n"+bridgeScript+"\n"+`} -AdapterName $p.adapter -ResultPath $p.result -VlanId $p.vlan -Confirm:$false`
}
func decodeBridgeResult(data []byte)(BridgeResult,error){var r BridgeResult;if e:=json.Unmarshal(data,&r);e!=nil{return r,fmt.Errorf("результат создания адаптера: %w",e)};if !r.OK{return r,fmt.Errorf("%s",r.Message)};if r.Interface!=VirtualAdapter{return r,fmt.Errorf("создан неожиданный интерфейс %q",r.Interface)};return r,nil}

func SplitAdapters(all []Adapter)(uplinks,virtual []Adapter){
 seen:=map[string]bool{}
 for _,a:=range all {
  if a.Physical && !seen[a.Name] {uplinks=append(uplinks,a);seen[a.Name]=true}
  if a.Name==VirtualAdapter && !a.Physical {virtual=append(virtual,a)}
 }
 return
}
