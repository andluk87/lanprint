package platform

import (
 "encoding/base64"
 "encoding/json"
 "strings"
 "testing"
)

func TestBridgeCommandEncodesParameters(t *testing.T){
 uplink:=`Ethernet'; Write-Host injected; '`;path:=`C:\Users\name\bridge.json`
 script:=bridgeCommand(uplink,path,123)
 if strings.Contains(script,uplink){t.Fatal("unescaped adapter interpolated into script")}
 b,_:=json.Marshal(map[string]any{"adapter":uplink,"result":path,"vlan":123})
 if !strings.Contains(script,base64.StdEncoding.EncodeToString(b)){t.Fatal("encoded parameters missing")}
 if !strings.Contains(script,"New-VMSwitch") || !strings.Contains(script,"Add-VMNetworkAdapter -ManagementOS") {t.Fatal("provisioning script not embedded")}
 if !strings.Contains(script,"-VlanId $p.vlan"){t.Fatal("VLAN parameter not forwarded")}
}
func TestBridgeResultRequiresDedicatedAdapter(t *testing.T){
 good:=[]byte(`{"ok":true,"interface":"vEthernet (LANPrint-Printer)","address":"192.168.1.55"}`)
 if _,e:=decodeBridgeResult(good);e!=nil{t.Fatal(e)}
 for _,bad:=range []string{`{"ok":false,"needs_reboot":true,"message":"restart Windows"}`,`{"ok":true,"interface":"Ethernet"}`,`#< CLIXML`}{if _,e:=decodeBridgeResult([]byte(bad));e==nil{t.Fatal("invalid provisioning result accepted")}}
}
func TestSplitAdaptersSeparatesUplinkAndEndpoint(t *testing.T){
 all:=[]Adapter{{Name:"Ethernet",Physical:true,Address:"192.168.1.10"},{Name:"Ethernet",Physical:true,Address:"192.168.1.11"},{Name:VirtualAdapter,Physical:false,Address:"192.168.1.55"},{Name:"vEthernet (Default Switch)"},{Name:VirtualAdapter,Physical:true}}
 uplinks,virtual:=SplitAdapters(all)
 if len(uplinks)!=2||len(virtual)!=1||virtual[0].Address!="192.168.1.55"{t.Fatal("physical NIC admitted as printer endpoint or uplink duplicated")}
}
