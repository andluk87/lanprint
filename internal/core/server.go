package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func secureEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

type Server struct {
	Cfg       Config
	Queue     *Queue
	listeners []net.Listener
	http      []*http.Server
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	slots     chan struct{}
	mu        sync.Mutex
	conns     map[net.Conn]bool
	discovery *Discovery
	log       func(string)
}

func Start(c Config, b Backend, log func(string)) (*Server, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	iface, e := net.InterfaceByName(c.Interface)
	if e != nil {
		return nil, e
	}
	addresses, e := iface.Addrs()
	if e != nil {
		return nil, e
	}
	found := false
	for _, a := range addresses {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip.Equal(net.ParseIP(c.Address)) {
			found = true
		}
	}
	if !found {
		return nil, errors.New("IP не назначен выбранному адаптеру")
	}
	if c.Mode == "bridge" && iface.Name != "vEthernet (LANPrint-Printer)" {
		return nil, errors.New("Bridge требует выделенный vEthernet (LANPrint...) адаптер из scripts/bridge.ps1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{Cfg: c, ctx: ctx, cancel: cancel, slots: make(chan struct{}, c.MaxClients), conns: map[net.Conn]bool{}, log: log}
	for _, p := range []int{c.RawPort, c.IPPPort, c.AdminPort} {
		l, e := net.Listen("tcp", net.JoinHostPort(c.Address, fmt.Sprint(p)))
		if e != nil {
			for _, l := range s.listeners {
				l.Close()
			}
			cancel()
			return nil, e
		}
		s.listeners = append(s.listeners, l)
	}
	s.Queue = NewQueue(c, b, log)
	s.wg.Add(1)
	go s.raw(s.listeners[0])
	ipp := &http.Server{Handler: IPPHandler(c, s.Queue, b), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	admin := &http.Server{Handler: s.admin(b), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	s.http = []*http.Server{ipp, admin}
	for i, h := range s.http {
		s.wg.Add(1)
		go func(h *http.Server, l net.Listener) {
			defer s.wg.Done()
			if e := h.Serve(s.limit(l)); e != nil && e != http.ErrServerClosed {
				log(e.Error())
			}
		}(h, s.listeners[i+1])
	}
	d, e := StartDiscovery(c, *iface, log)
	if e != nil {
		log("mDNS unavailable: " + e.Error())
	} else {
		s.discovery = d
	}
	log("services started at " + c.Address)
	return s, nil
}
func (s *Server) raw(l net.Listener) {
	defer s.wg.Done()
	for {
		c, e := l.Accept()
		if e != nil {
			return
		}
		if !Allowed(s.Cfg, c.RemoteAddr().String()) {
			c.Close()
			continue
		}
		select {
		case s.slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		s.mu.Lock()
		s.conns[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { c.Close(); <-s.slots; s.mu.Lock(); delete(s.conns, c); s.mu.Unlock() }()
			c.SetReadDeadline(time.Now().Add(30 * time.Second))
			data, e := io.ReadAll(io.LimitReader(c, s.Cfg.MaxJobBytes+1))
			if e != nil {
				s.log("RAW read: " + e.Error())
				return
			}
			if len(data) == 0 {
				return
			}
			_, e = s.Queue.Submit("RAW document", c.RemoteAddr().String(), s.Cfg.RawFormat, data)
			if e != nil {
				s.log("RAW rejected: " + e.Error())
			}
		}()
	}
}

type limitedListener struct {
	net.Listener
	s *Server
}
type limitedConn struct {
	net.Conn
	once sync.Once
	s    *Server
}

func (c *limitedConn) Close() error { e := c.Conn.Close(); c.once.Do(func() { <-c.s.slots }); return e }
func (l limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		if !Allowed(l.s.Cfg, c.RemoteAddr().String()) {
			c.Close()
			continue
		}
		select {
		case l.s.slots <- struct{}{}:
			return &limitedConn{Conn: c, s: l.s}, nil
		default:
			c.Close()
		}
	}
}
func (s *Server) limit(l net.Listener) net.Listener { return limitedListener{l, s} }
func (s *Server) admin(b Backend) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
		if !Allowed(s.Cfg, r.RemoteAddr) {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.URL.Path == "/" && r.Method == "GET" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, adminPage)
			return
		}
		if !authorized(s.Cfg, r) {
			http.Error(w, "Bearer token required", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status" && r.Method == "GET":
			status, e := b.Status(s.Cfg.Printer)
			errText := ""
			if e != nil {
				errText = e.Error()
			}
			json.NewEncoder(w).Encode(map[string]any{"name": s.Cfg.Name, "address": s.Cfg.Address, "printer": s.Cfg.Printer, "status": status, "error": errText, "jobs": s.Queue.List()})
		case strings.HasPrefix(r.URL.Path, "/api/jobs/") && r.Method == "DELETE":
			id, e := parseJobID(strings.TrimPrefix(r.URL.Path, "/api/jobs/"))
			if e == nil {
				e = s.Queue.Cancel(id)
			}
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	})
	return m
}
func (s *Server) Stop() {
	s.cancel()
	for _, l := range s.listeners {
		l.Close()
	}
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	for _, h := range s.http {
		h.Close()
	}
	if s.discovery != nil {
		s.discovery.Stop()
	}
	s.wg.Wait()
	s.Queue.Stop()
	s.log("services stopped")
}

const adminPage = `<!doctype html><meta charset="utf-8"><title>LANPrint</title><style>body{font:16px system-ui;max-width:900px;margin:40px auto}input{width:70%}pre{white-space:pre-wrap}</style><h1>LANPrint</h1><p>Токен из окна приложения. HTTP используйте только в доверенной LAN.</p><input id="token" type="password" autocomplete="off" placeholder="Токен администратора"><button id="refresh">Обновить</button><pre id="out"></pre><input id="job" type="number" placeholder="ID задания"><button id="cancel">Отменить</button><script>const auth=()=>({'Authorization':'Bearer '+document.getElementById('token').value});async function load(){const r=await fetch('/api/status',{headers:auth()});document.getElementById('out').textContent=await r.text()}document.getElementById('refresh').onclick=load;document.getElementById('cancel').onclick=async()=>{const r=await fetch('/api/jobs/'+document.getElementById('job').value,{method:'DELETE',headers:auth()});document.getElementById('out').textContent=await r.text()};</script>`
