//go:build !windows

package platform
import "fmt"
func CreateBridge(string,int)(BridgeResult,error){return BridgeResult{},fmt.Errorf("создание Hyper-V адаптера доступно только в Windows")}
