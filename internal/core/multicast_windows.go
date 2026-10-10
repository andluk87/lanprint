//go:build windows

package core

import (
	"net"
	"syscall"
)

func setMulticastInterface(c *net.UDPConn, ip net.IP) error {
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var result error
	e = raw.Control(func(fd uintptr) {
		result = syscall.SetsockoptInet4Addr(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, [4]byte{ip[0], ip[1], ip[2], ip[3]})
		if result == nil {
			result = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 255)
		}
	})
	if e != nil {
		return e
	}
	return result
}
