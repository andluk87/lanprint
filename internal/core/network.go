package core

import (
	"errors"
	"net"
	"strings"
)

func ValidateNetwork(ip, mask, gateway, dns string) error {
	addr := net.ParseIP(ip).To4()
	m := net.ParseIP(mask).To4()
	if addr == nil || m == nil || addr.IsUnspecified() || addr.IsMulticast() {
		return errors.New("неверный IPv4 или маска")
	}
	prefix, bits := net.IPMask(m).Size()
	if bits != 32 || prefix < 1 || prefix > 30 {
		return errors.New("нужна непрерывная маска /1–/30")
	}
	network := addr.Mask(net.IPMask(m))
	broadcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		broadcast[i] = network[i] | ^m[i]
	}
	if addr.Equal(network) || addr.Equal(broadcast) {
		return errors.New("нельзя использовать адрес сети или broadcast")
	}
	if gateway != "" {
		g := net.ParseIP(gateway).To4()
		if g == nil || !g.Mask(net.IPMask(m)).Equal(network) || g.Equal(addr) || g.Equal(network) || g.Equal(broadcast) {
			return errors.New("шлюз должен быть другим адресом этой подсети")
		}
	}
	if dns != "" {
		for _, s := range strings.Split(dns, ",") {
			if net.ParseIP(strings.TrimSpace(s)).To4() == nil {
				return errors.New("неверный DNS IPv4")
			}
		}
	}
	return nil
}
