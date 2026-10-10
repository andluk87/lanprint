package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu                sync.Mutex
	docs              [][]byte
	active, maxActive int
	block             chan struct{}
	entered           chan struct{}
	fail              bool
	cancelled         []uint32
}

func (b *fakeBackend) Print(ctx context.Context, _ string, _ string, data []byte) (uint32, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	b.docs = append(b.docs, append([]byte(nil), data...))
	id := uint32(len(b.docs))
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	if b.entered != nil {
		select {
		case b.entered <- struct{}{}:
		default:
		}
	}
	if b.block != nil {
		select {
		case <-b.block:
		case <-ctx.Done():
			return id, ctx.Err()
		}
	}
	if b.fail {
		return id, errors.New("printer unplugged")
	}
	return id, nil
}
func (b *fakeBackend) Cancel(_ string, id uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelled = append(b.cancelled, id)
	return nil
}
func (b *fakeBackend) Status(string) (string, error) { return "ready", nil }
func config() Config {
	c := Default()
	c.Interface = "lo"
	c.Address = "127.0.0.1"
	c.Printer = "fake"
	return c
}
func wait(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("timeout")
}
func TestConfigSecurity(t *testing.T) {
	c := config()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, ip := range []string{"0.0.0.0", "224.0.0.251", "not-ip", "::1"} {
		v := c
		v.Address = ip
		if v.Validate() == nil {
			t.Fatalf("accepted %s", ip)
		}
	}
	if !Allowed(c, "192.168.2.4:42") || Allowed(c, "8.8.8.8:42") {
		t.Fatal("ACL")
	}
	path := t.TempDir() + "/config.json"
	if e := Save(path, c); e != nil {
		t.Fatal(e)
	}
	v, e := Load(path)
	if e != nil || v.Token != c.Token {
		t.Fatal("config roundtrip")
	}
}

func TestWindowsEndpointCannotUsePhysicalNIC(t *testing.T) {
	for _,v:=range [][2]string{{"shared","Ethernet"},{"bridge","Ethernet"},{"bridge","vEthernet (Default Switch)"},{"bridge",""}} {
		if validateEndpoint(v[0],v[1],true)==nil { t.Fatalf("host endpoint accepted: %v",v) }
	}
	if e:=validateEndpoint("bridge",VirtualAdapter,true);e!=nil {t.Fatal(e)}
}

func TestVLANIDBoundaries(t *testing.T) {
	for _,id:=range []int{0,1,100,4094}{c:=config();c.VLANID=id;if e:=c.Validate();e!=nil{t.Fatalf("valid VLAN %d: %v",id,e)}}
	for _,id:=range []int{-1,4095,65535}{c:=config();c.VLANID=id;if c.Validate()==nil{t.Fatalf("reserved VLAN %d accepted",id)}}
}

func TestLegacySharedConfigMigratesWithoutReusingHostIP(t *testing.T) {
	path:=t.TempDir()+"/config.json"
	data:=[]byte(`{"name":"Office","printer":"USB Printer","interface":"Ethernet","address":"192.168.1.10","mode":"shared","admin_token":"01234567890123456789012345678901"}`)
	if e:=os.WriteFile(path,data,0600);e!=nil{t.Fatal(e)}
	c,e:=Load(path);if e!=nil{t.Fatal(e)}
	if c.Mode!="bridge"||c.Interface!=VirtualAdapter||c.Address!=""||c.Printer!="USB Printer"||c.Token!="01234567890123456789012345678901" {t.Fatalf("bad migration: %+v",c)}
	unchanged,e:=os.ReadFile(path);if e!=nil||!bytes.Equal(unchanged,data){t.Fatal("loading modified user settings on disk")}
}
func TestNetworkValidation(t *testing.T) {
	if e := ValidateNetwork("192.168.1.44", "255.255.255.0", "192.168.1.1", "1.1.1.1,8.8.8.8"); e != nil {
		t.Fatal(e)
	}
	for _, v := range [][4]string{{"192.168.1.0", "255.255.255.0", "", ""}, {"192.168.1.255", "255.255.255.0", "", ""}, {"192.168.1.2", "255.0.255.0", "", ""}, {"192.168.1.2", "255.255.255.0", "192.168.2.1", ""}, {"192.168.1.2", "255.255.255.0", "", "invalid"}} {
		if ValidateNetwork(v[0], v[1], v[2], v[3]) == nil {
			t.Fatal(v)
		}
	}
}
func TestDocumentFormats(t *testing.T) {
	if ValidateDocument("text/plain", []byte{0xff}) == nil {
		t.Fatal("invalid UTF8")
	}
	if ValidateDocument("application/pdf", []byte("%PDF")) == nil {
		t.Fatal("PDF accepted")
	}
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if e := ValidateDocument("image/png", b.Bytes()); e != nil {
		t.Fatal(e)
	}
	if ValidateDocument("image/jpeg", b.Bytes()) == nil {
		t.Fatal("mismatched format")
	}
}
func TestQueueSerialAndCancellation(t *testing.T) {
	b := &fakeBackend{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	c := config()
	c.MaxQueued = 1
	q := NewQueue(c, b, func(string) {})
	defer q.Stop()
	first, e := q.Submit("first", "client", "text/plain", []byte("one"))
	if e != nil {
		t.Fatal(e)
	}
	<-b.entered
	second, e := q.Submit("second", "client", "text/plain", []byte("two"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = q.Submit("overflow", "client", "text/plain", []byte("three")); e == nil {
		t.Fatal("queue overflow accepted")
	}
	if e = q.Cancel(second.ID); e != nil {
		t.Fatal(e)
	}
	close(b.block)
	wait(t, func() bool { j, _ := q.Get(first.ID); return j.State == "spooled" })
	if e = q.Cancel(first.ID); e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.docs) != 1 || b.maxActive != 1 || len(b.cancelled) != 1 {
		t.Fatal("serial/cancel contract")
	}
}
func TestQueueLimitsAndFailure(t *testing.T) {
	c := config()
	c.MaxJobBytes = 4
	b := &fakeBackend{fail: true}
	q := NewQueue(c, b, func(string) {})
	defer q.Stop()
	if _, e := q.Submit("oversize", "client", "text/plain", []byte("12345")); e == nil {
		t.Fatal("size")
	}
	if _, e := q.Submit("raw", "client", "application/octet-stream", []byte("raw")); e == nil {
		t.Fatal("unconfigured raw")
	}
	j, e := q.Submit("text", "client", "text/plain", []byte("123"))
	if e != nil {
		t.Fatal(e)
	}
	wait(t, func() bool {
		v, _ := q.Get(j.ID)
		return v.State == "aborted" && strings.Contains(v.Error, "unplugged")
	})
}
func TestQueueStopCancelsActive(t *testing.T) {
	b := &fakeBackend{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	q := NewQueue(config(), b, func(string) {})
	q.Submit("first", "client", "text/plain", []byte("abc"))
	<-b.entered
	q.Stop()
	if _, e := q.Submit("after", "client", "text/plain", []byte("abc")); e == nil {
		t.Fatal("submit after stop")
	}
}
func requestIPP(op uint16, attrs []Attr, doc []byte) []byte {
	w := &ippWriter{}
	w.Write([]byte{1, 1, byte(op >> 8), byte(op), 0, 0, 0, 1, 1})
	w.str(0x47, "attributes-charset", "utf-8")
	w.str(0x48, "attributes-natural-language", "en")
	w.str(0x45, "printer-uri", "ipp://127.0.0.1/ipp/print")
	for _, a := range attrs {
		w.attr(a.Tag, a.Name, a.Value)
	}
	w.WriteByte(3)
	w.Write(doc)
	return w.Bytes()
}
func TestIPPFunctional(t *testing.T) {
	c := config()
	b := &fakeBackend{}
	q := NewQueue(c, b, func(string) {})
	defer q.Stop()
	h := IPPHandler(c, q, b)
	post := func(body []byte, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/ipp/print", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:33"
		r.Header.Set("Content-Type", "application/ipp")
		r.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	w := post(requestIPP(2, []Attr{{0x49, "document-format", []byte("text/plain")}, {0x42, "job-name", []byte("test")}}, []byte("hello")), "")
	p, e := DecodeIPP(w.Body.Bytes())
	if w.Code != 200 || e != nil || p.Code != 0 || p.Number("job-id") != 1 {
		t.Fatalf("print %d %v %+v", w.Code, e, p)
	}
	wait(t, func() bool { j, _ := q.Get(1); return j.State == "spooled" })
	b.mu.Lock()
	if string(b.docs[0]) != "hello" {
		t.Fatal("document not delivered")
	}
	b.mu.Unlock()
	w = post(requestIPP(11, nil, nil), "")
	p, e = DecodeIPP(w.Body.Bytes())
	if e != nil || p.Value("printer-name") != c.Name {
		t.Fatal("attributes")
	}
	w = post(requestIPP(2, []Attr{{0x49, "document-format", []byte("application/pdf")}}, []byte("%PDF")), "")
	p, _ = DecodeIPP(w.Body.Bytes())
	if p.Code != 0x040a {
		t.Fatal("unsupported format")
	}
	id := make([]byte, 4)
	binary.BigEndian.PutUint32(id, 1)
	w = post(requestIPP(8, []Attr{{0x21, "job-id", id}}, nil), "")
	p, _ = DecodeIPP(w.Body.Bytes())
	if p.Code != 0x0403 {
		t.Fatal("cancel authorization")
	}
	w = post(requestIPP(8, []Attr{{0x21, "job-id", id}}, nil), "Bearer "+c.Token)
	p, _ = DecodeIPP(w.Body.Bytes())
	if p.Code != 0 {
		t.Fatal("authorized cancel")
	}
}
func TestIPPParserRejectsMalformed(t *testing.T) {
	valid := requestIPP(11, nil, nil)
	for n := 0; n < len(valid); n++ {
		if _, e := DecodeIPP(valid[:n]); e == nil {
			t.Fatalf("truncated accepted %d", n)
		}
	}
	if _, e := DecodeIPP([]byte{1, 1, 0, 2, 0, 0, 0, 1, 0x42, 0xff, 0xff}); e == nil {
		t.Fatal("overflow")
	}
}
func TestDNSPacket(t *testing.T) {
	b := discoveryPacket(config(), 120)
	if binary.BigEndian.Uint16(b[6:]) != 7 {
		t.Fatal("record count")
	}
	pos := 12
	for i := 0; i < 7; i++ {
		_, end, e := readDNSName(b, pos)
		if e != nil || end+10 > len(b) {
			t.Fatal(e)
		}
		size := int(binary.BigEndian.Uint16(b[end+8:]))
		pos = end + 10 + size
		if pos > len(b) {
			t.Fatal("RDATA")
		}
	}
	if pos != len(b) {
		t.Fatal("trailing DNS bytes")
	}
	if _, _, e := readDNSName([]byte{0xc0, 0}, 0); e == nil {
		t.Fatal("compression cycle")
	}
}
func freePort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func TestServersEndToEndAndReleasePorts(t *testing.T) {
	c := config()
	c.RawPort = freePort(t)
	for {
		c.IPPPort = freePort(t)
		if c.IPPPort != c.RawPort {
			break
		}
	}
	for {
		c.AdminPort = freePort(t)
		if c.AdminPort != c.RawPort && c.AdminPort != c.IPPPort {
			break
		}
	}
	b := &fakeBackend{}
	s, e := Start(c, b, func(string) {})
	if e != nil {
		t.Fatal(e)
	}
	stopped := false
	defer func() {
		if !stopped {
			s.Stop()
		}
	}()
	conn, e := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", c.RawPort))
	if e != nil {
		t.Fatal(e)
	}
	conn.Write([]byte("RAW text test"))
	conn.Close()
	wait(t, func() bool { jobs := s.Queue.List(); return len(jobs) == 1 && jobs[0].State == "spooled" })
	url := fmt.Sprintf("http://127.0.0.1:%d/api/status", c.AdminPort)
	client := &http.Client{Timeout: time.Second}
	resp, e := client.Get(url)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("admin auth")
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var status map[string]any
	if json.Unmarshal(data, &status) != nil || status["printer"] != "fake" {
		t.Fatal("admin status")
	}
	resp, e = client.Post(fmt.Sprintf("http://127.0.0.1:%d/ipp/print", c.IPPPort), "application/ipp", bytes.NewReader(requestIPP(11, nil, nil)))
	if e != nil {
		t.Fatal(e)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	p, e := DecodeIPP(data)
	if e != nil || p.Code != 0 {
		t.Fatal("live IPP")
	}
	s.Stop()
	stopped = true
	for _, p := range []int{c.RawPort, c.IPPPort, c.AdminPort} {
		l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if e != nil {
			t.Fatal("port not released", e)
		}
		l.Close()
	}
}
func FuzzDecodeIPP(f *testing.F) {
	f.Add(requestIPP(2, nil, []byte("hi")))
	f.Add([]byte{1, 1, 0, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		DecodeIPP(b)
	})
}

func TestConcurrentClientsAreSerialized(t *testing.T) {
	c := config()
	c.MaxClients = 16
	c.RawPort = freePort(t)
	c.IPPPort = freePort(t)
	c.AdminPort = freePort(t)
	b := &fakeBackend{block: make(chan struct{})}
	s, e := Start(c, b, func(string) {})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	var clients sync.WaitGroup
	failures := make(chan error, 6)
	for i := 0; i < 6; i++ {
		clients.Add(1)
		go func(i int) {
			defer clients.Done()
			conn, e := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", c.RawPort))
			if e != nil {
				failures <- e
				return
			}
			defer conn.Close()
			_, e = fmt.Fprintf(conn, "client %d", i)
			if e != nil {
				failures <- e
			}
		}(i)
	}
	clients.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	wait(t, func() bool { return len(s.Queue.List()) == 6 })
	close(b.block)
	wait(t, func() bool {
		for _, j := range s.Queue.List() {
			if j.State != "spooled" {
				return false
			}
		}
		return true
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.docs) != 6 || b.maxActive != 1 {
		t.Fatal("clients were not sequentially spooled")
	}
}
func TestDeniedIPPAndOversizedUpload(t *testing.T) {
	c := config()
	c.MaxJobBytes = 4
	b := &fakeBackend{}
	q := NewQueue(c, b, func(string) {})
	defer q.Stop()
	h := IPPHandler(c, q, b)
	r := httptest.NewRequest("POST", "/ipp/print", bytes.NewReader(requestIPP(11, nil, nil)))
	r.Header.Set("Content-Type", "application/ipp")
	r.RemoteAddr = "8.8.8.8:42"
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 403 {
		t.Fatal("ACL bypass")
	}
	r = httptest.NewRequest("POST", "/ipp/print", bytes.NewReader(make([]byte, 65541)))
	r.Header.Set("Content-Type", "application/ipp")
	r.RemoteAddr = "127.0.0.1:42"
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != 413 {
		t.Fatal("body limit bypass")
	}
}
