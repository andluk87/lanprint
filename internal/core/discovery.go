package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

type Discovery struct {
	rx, tx *net.UDPConn
	done   chan struct{}
	wg     sync.WaitGroup
	c      Config
	log    func(string)
}

func dnsName(s string) []byte {
	var b []byte
	for _, v := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(v) > 63 {
			return nil
		}
		b = append(b, byte(len(v)))
		b = append(b, v...)
	}
	return append(b, 0)
}
func readDNSName(b []byte, pos int) (string, int, error) {
	parts := []string{}
	end := 0
	for hops := 0; hops < 32; hops++ {
		if pos >= len(b) {
			break
		}
		n := int(b[pos])
		pos++
		if n == 0 {
			if end == 0 {
				end = pos
			}
			return strings.Join(parts, "."), end, nil
		}
		if n&192 == 192 {
			if pos >= len(b) {
				break
			}
			ptr := (n&63)<<8 | int(b[pos])
			if end == 0 {
				end = pos + 1
			}
			pos = ptr
			continue
		}
		if n > 63 || pos+n > len(b) {
			break
		}
		parts = append(parts, string(b[pos:pos+n]))
		pos += n
	}
	return "", 0, errors.New("invalid DNS name")
}
func discoveryPacket(c Config, ttl uint32) []byte {
	var b bytes.Buffer
	b.Write(make([]byte, 12))
	binary.BigEndian.PutUint16(b.Bytes()[2:], 0x8400)
	binary.BigEndian.PutUint16(b.Bytes()[6:], 7)
	instance := c.Name + "._ipp._tcp.local"
	host := "lanprint-" + strings.ReplaceAll(c.Address, ".", "-") + ".local"
	record := func(name string, kind uint16, data []byte, unique bool) {
		b.Write(dnsName(name))
		binary.Write(&b, binary.BigEndian, kind)
		class := uint16(1)
		if unique {
			class = 0x8001
		}
		binary.Write(&b, binary.BigEndian, class)
		binary.Write(&b, binary.BigEndian, ttl)
		binary.Write(&b, binary.BigEndian, uint16(len(data)))
		b.Write(data)
	}
	record("_services._dns-sd._udp.local", 12, dnsName("_ipp._tcp.local"), false)
	record("_ipp._tcp.local", 12, dnsName(instance), false)
	srv := []byte{0, 0, 0, 0, byte(c.IPPPort >> 8), byte(c.IPPPort)}
	srv = append(srv, dnsName(host)...)
	record(instance, 33, srv, true)
	var txt []byte
	for _, s := range []string{"txtvers=1", "qtotal=1", "rp=ipp/print", "ty=LANPrint Windows", "pdl=text/plain,image/jpeg,image/png"} {
		txt = append(txt, byte(len(s)))
		txt = append(txt, s...)
	}
	record(instance, 16, txt, true)
	record(host, 1, net.ParseIP(c.Address).To4(), true)
	raw := c.Name + "._pdl-datastream._tcp.local"
	record("_pdl-datastream._tcp.local", 12, dnsName(raw), false)
	srv = []byte{0, 0, 0, 0, byte(c.RawPort >> 8), byte(c.RawPort)}
	srv = append(srv, dnsName(host)...)
	record(raw, 33, srv, true)
	return b.Bytes()
}
func StartDiscovery(c Config, iface net.Interface, log func(string)) (*Discovery, error) {
	rx, e := net.ListenMulticastUDP("udp4", &iface, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	if e != nil {
		return nil, e
	}
	if e = setMulticastInterface(rx, net.ParseIP(c.Address).To4()); e != nil {
		rx.Close()
		return nil, e
	}
	d := &Discovery{rx: rx, tx: rx, done: make(chan struct{}), c: c, log: log}
	d.wg.Add(1)
	go d.run()
	return d, nil
}
func (d *Discovery) send(ttl uint32) {
	_, e := d.tx.WriteToUDP(discoveryPacket(d.c, ttl), &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	if e != nil {
		d.log("mDNS: " + e.Error())
	}
}
func (d *Discovery) run() {
	defer d.wg.Done()
	d.send(120)
	buf := make([]byte, 9000)
	last := time.Now()
	next := time.Now().Add(60 * time.Second)
	for {
		select {
		case <-d.done:
			return
		default:
		}
		d.rx.SetReadDeadline(time.Now().Add(time.Second))
		n, remote, e := d.rx.ReadFromUDP(buf)
		if time.Now().After(next) {
			d.send(120)
			next = time.Now().Add(60 * time.Second)
		}
		if e != nil {
			continue
		}
		if n < 12 || buf[2]&128 != 0 || !Allowed(d.c, remote.String()) {
			continue
		}
		count := int(binary.BigEndian.Uint16(buf[4:6]))
		if count > 32 {
			continue
		}
		pos := 12
		match := false
		for i := 0; i < count; i++ {
			name, end, e := readDNSName(buf[:n], pos)
			if e != nil || end+4 > n {
				break
			}
			pos = end + 4
			if name == "_services._dns-sd._udp.local" || strings.HasSuffix(strings.ToLower(name), "._tcp.local") || strings.HasPrefix(name, "lanprint-") {
				match = true
			}
		}
		if match && time.Since(last) > 250*time.Millisecond {
			d.send(120)
			last = time.Now()
		}
	}
}
func (d *Discovery) Stop() { close(d.done); d.send(0); d.rx.Close(); d.wg.Wait(); d.tx.Close() }
func DiscoveryDescription(c Config) string {
	return fmt.Sprintf("%s via IPP %s:%d", c.Name, c.Address, c.IPPPort)
}
