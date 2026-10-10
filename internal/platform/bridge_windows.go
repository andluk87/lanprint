//go:build windows

package platform

import("encoding/base64";"encoding/binary";"fmt";"os";"path/filepath";"syscall";"unicode/utf16";"unsafe")

type shellExecuteInfo struct {
 Size,Mask uint32
 Window uintptr
 Verb,File,Parameters,Directory *uint16
 Show int32
 Instance,IDList uintptr
 Class *uint16
 ClassKey uintptr
 HotKey uint32
 Icon,Process uintptr
}
func CreateBridge(uplink string,vlan int)(BridgeResult,error){
 var empty BridgeResult
 if vlan<0||vlan>4094{return empty,fmt.Errorf("VLAN ID: 0 (без тега) или 1–4094")}
 if uplink==""{return empty,fmt.Errorf("выберите физический Ethernet для подключения виртуального адаптера к LAN")}
 root:=filepath.Join(os.Getenv("LOCALAPPDATA"),"LANPrint")
 if e:=os.MkdirAll(root,0700);e!=nil{return empty,e}
 f,e:=os.CreateTemp(root,"bridge-result-*.json");if e!=nil{return empty,e};path:=f.Name();f.Close();defer os.Remove(path)
 script:=bridgeCommand(uplink,path,vlan)
 words:=utf16.Encode([]rune(script));b:=make([]byte,len(words)*2);for i,v:=range words{binary.LittleEndian.PutUint16(b[i*2:],v)}
 info:=shellExecuteInfo{Mask:0x40|0x100,Verb:ptr("runas"),File:ptr("powershell.exe"),Parameters:ptr("-NoLogo -NoProfile -NonInteractive -EncodedCommand "+base64.StdEncoding.EncodeToString(b)),Show:1}
 info.Size=uint32(unsafe.Sizeof(info));r,_,last:=shell.NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
 if r==0{return empty,fmt.Errorf("создание адаптера отменено или UAC не разрешён: %v",last)}
 handle:=syscall.Handle(info.Process);if handle==0{return empty,fmt.Errorf("не удалось дождаться процесса настройки")};defer syscall.CloseHandle(handle)
 // The elevated helper owns network changes. Never kill it mid-configuration.
 status,e:=syscall.WaitForSingleObject(handle,syscall.INFINITE);if e!=nil||status!=syscall.WAIT_OBJECT_0{return empty,fmt.Errorf("ожидание настройки: %v",e)}
 var exit uint32;if e=syscall.GetExitCodeProcess(handle,&exit);e!=nil{return empty,e}
 data,e:=os.ReadFile(path);if e!=nil{return empty,e}
 result,e:=decodeBridgeResult(data);if e!=nil{return result,e};if exit!=0{return result,fmt.Errorf("настройка завершилась с кодом %d",exit)}
 if result.VLANID!=vlan{return result,fmt.Errorf("помощник применил другую VLAN: %d",result.VLANID)}
 return result,nil
}
