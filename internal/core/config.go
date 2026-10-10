package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const Version = "0.1.0"

type Config struct {
	Name        string   `json:"name"`
	Printer     string   `json:"printer"`
	Interface   string   `json:"interface"`
	Address     string   `json:"address"`
	Mode        string   `json:"mode"`
	RawFormat   string   `json:"raw_format"`
	Allowed     []string `json:"allowed_cidrs"`
	MaxJobBytes int64    `json:"max_job_bytes"`
	MaxClients  int      `json:"max_clients"`
	MaxQueued   int      `json:"max_queued"`
	RawPort     int      `json:"raw_port"`
	IPPPort     int      `json:"ipp_port"`
	AdminPort   int      `json:"admin_port"`
	Token       string   `json:"admin_token"`
	AutoStart   bool     `json:"auto_start"`
}

func Default() Config {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return Config{Name: "LANPrint", Mode: "shared", RawFormat: "text/plain", Allowed: []string{"192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "127.0.0.0/8"}, MaxJobBytes: 32 << 20, MaxClients: 8, MaxQueued: 32, RawPort: 9100, IPPPort: 8631, AdminPort: 8765, Token: hex.EncodeToString(b)}
}
func (c Config) Validate() error {
	if c.Name == "" || len(c.Name) > 63 {
		return errors.New("имя принтера должно содержать 1–63 байта")
	}
	if c.Mode != "shared" && c.Mode != "bridge" {
		return errors.New("режим: shared или bridge")
	}
	if c.Interface == "" || net.ParseIP(c.Address).To4() == nil || net.ParseIP(c.Address).IsUnspecified() || net.ParseIP(c.Address).IsMulticast() {
		return errors.New("выберите интерфейс и его конкретный IPv4")
	}
	if c.Printer == "" {
		return errors.New("выберите физический принтер")
	}
	if c.MaxJobBytes < 1 || c.MaxJobBytes > 128<<20 || c.MaxClients < 1 || c.MaxClients > 64 || c.MaxQueued < 1 || c.MaxQueued > 128 {
		return errors.New("недопустимые лимиты")
	}
	if len(c.Token) < 32 {
		return errors.New("токен администратора слишком короткий")
	}
	if c.RawFormat != "text/plain" && c.RawFormat != "application/octet-stream" {
		return errors.New("RAW: text/plain или application/octet-stream")
	}
	if len(c.Allowed) == 0 {
		return errors.New("задайте разрешённые подсети")
	}
	for _, s := range c.Allowed {
		if _, _, e := net.ParseCIDR(s); e != nil {
			return fmt.Errorf("CIDR %q: %w", s, e)
		}
	}
	ports := map[int]bool{}
	for _, p := range []int{c.RawPort, c.IPPPort, c.AdminPort} {
		if p < 1 || p > 65535 || ports[p] {
			return errors.New("порты должны быть различными, 1–65535")
		}
		ports[p] = true
	}
	return nil
}
func Load(path string) (Config, error) {
	c := Default()
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func Save(path string, c Config) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func Allowed(c Config, remote string) bool {
	h, _, e := net.SplitHostPort(remote)
	if e != nil {
		h = remote
	}
	ip := net.ParseIP(h)
	for _, s := range c.Allowed {
		_, n, e := net.ParseCIDR(s)
		if e == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
