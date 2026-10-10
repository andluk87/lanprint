//go:build windows

package gui

import (
	"fmt"
	"github.com/andluk87/lanprint/internal/core"
	"github.com/andluk87/lanprint/internal/platform"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var user = syscall.NewLazyDLL("user32.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var shell = syscall.NewLazyDLL("shell32.dll")

func u(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

//go:uintptrescapes
func send(h uintptr, m uint32, w, l uintptr) uintptr {
	r, _, _ := user.NewProc("SendMessageW").Call(h, uintptr(m), w, l)
	return r
}
func set(h uintptr, s string) { user.NewProc("SetWindowTextW").Call(h, uintptr(unsafe.Pointer(u(s)))) }
func text(h uintptr) string {
	b := make([]uint16, 8192)
	r, _, _ := user.NewProc("GetWindowTextW").Call(h, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	return syscall.UTF16ToString(b[:r])
}

type point struct{ X, Y int32 }
type message struct {
	Hwnd           uintptr
	Message        uint32
	Wparam, Lparam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type notifyIcon struct {
	Size                uint32
	Hwnd                uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	Guid                [16]byte
	Balloon             uintptr
}
type result struct {
	fn  func()
	err error
}
type application struct {
	hwnd          uintptr
	controls      map[int]uintptr
	cfg           core.Config
	path          string
	log           func(string)
	logs          <-chan string
	lines         []string
	adapters      []platform.Adapter
	uplinks       []platform.Adapter
	printers      []platform.Printer
	server        *core.Server
	desired, busy bool
	results       chan result
	tick          int
	icon          notifyIcon
}

var app *application
var errVirtualIP = fmt.Errorf("виртуальный адаптер ожидает IPv4 из LAN; проверьте DHCP или примените Static IP")

const (
	netCombo     = 101
	printerCombo = 102
	nameEdit     = 103
	rawCheck     = 105
	autoCheck    = 106
	ipEdit       = 107
	maskEdit     = 108
	gwEdit       = 109
	dnsEdit      = 110
	netInfo      = 111
	tokenEdit    = 112
	logEdit      = 113
	statusEdit   = 114
	jobEdit      = 115
	uplinkCombo  = 116
	vlanEdit     = 117
)

func (a *application) control(class, label string, id, x, y, w, h int, style uint32) uintptr {
	v, _, _ := user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(u(class))), uintptr(unsafe.Pointer(u(label))), uintptr(style|0x50000000), uintptr(x), uintptr(y), uintptr(w), uintptr(h), a.hwnd, uintptr(id), 0, 0)
	a.controls[id] = v
	return v
}
func (a *application) label(s string, x, y, w int) { a.control("STATIC", s, 0, x, y, w, 22, 0) }
func (a *application) edit(s string, id, x, y, w int) {
	a.control("EDIT", s, id, x, y, w, 25, 0x00800080)
}
func (a *application) button(s string, id, x, y, w int) { a.control("BUTTON", s, id, x, y, w, 28, 0) }
func (a *application) check(s string, id, x, y, w int, value bool) {
	h := a.control("BUTTON", s, id, x, y, w, 24, 3)
	if value {
		send(h, 0xf1, 1, 0)
	}
}
func (a *application) checked(id int) bool { return send(a.controls[id], 0xf0, 0, 0) == 1 }
func (a *application) error(e error) {
	if e != nil {
		a.log(e.Error())
		user.NewProc("MessageBoxW").Call(a.hwnd, uintptr(unsafe.Pointer(u(e.Error()))), uintptr(unsafe.Pointer(u("LANPrint"))), 0x10)
	}
}
func (a *application) confirm(s string) bool {
	r, _, _ := user.NewProc("MessageBoxW").Call(a.hwnd, uintptr(unsafe.Pointer(u(s))), uintptr(unsafe.Pointer(u("LANPrint — подтверждение"))), 0x34)
	return r == 6
}
func (a *application) async(work func() result) {
	if a.busy {
		return
	}
	a.busy = true
	go func() { a.results <- work() }()
}
func (a *application) populate() {
	send(a.controls[uplinkCombo], 0x14b, 0, 0)
	uplink := 0
	for i, v := range a.uplinks {
		send(a.controls[uplinkCombo], 0x143, 0, uintptr(unsafe.Pointer(u(v.Name))))
		if v.Name == a.cfg.Uplink { uplink = i }
	}
	send(a.controls[uplinkCombo], 0x14e, uintptr(uplink), 0)
	send(a.controls[netCombo], 0x14b, 0, 0)
	chosen := 0
	for i, v := range a.adapters {
		label := fmt.Sprintf("%s — %s", v.Name, v.Address)
		send(a.controls[netCombo], 0x143, 0, uintptr(unsafe.Pointer(u(label))))
		if v.Name == a.cfg.Interface && (chosen == 0 || v.Address == a.cfg.Address) {
			chosen = i
		}
	}
	send(a.controls[netCombo], 0x14e, uintptr(chosen), 0)
	a.selectAdapter()
	if len(a.adapters) == 0 { set(a.controls[netInfo], "Виртуальный адаптер ещё не создан. Выберите Ethernet и нажмите «Создать адаптер». ") }
	send(a.controls[printerCombo], 0x14b, 0, 0)
	chosen = 0
	for i, v := range a.printers {
		send(a.controls[printerCombo], 0x143, 0, uintptr(unsafe.Pointer(u(v.Name))))
		if v.Name == a.cfg.Printer {
			chosen = i
		}
	}
	send(a.controls[printerCombo], 0x14e, uintptr(chosen), 0)
}
func (a *application) selectAdapter() {
	i := int(send(a.controls[netCombo], 0x147, 0, 0))
	if i < 0 || i >= len(a.adapters) {
		return
	}
	v := a.adapters[i]
	set(a.controls[ipEdit], v.Address)
	set(a.controls[maskEdit], v.Mask)
	set(a.controls[gwEdit], v.Gateway)
	set(a.controls[netInfo], fmt.Sprintf("MAC %s | %s | DHCP %t | подключён %t", v.MAC, v.Description, v.DHCP, v.Up))
}
func (a *application) read() (core.Config, error) {
	c := a.cfg
	c.Name = text(a.controls[nameEdit])
	c.Token = text(a.controls[tokenEdit])
	c.AutoStart = a.checked(autoCheck)
	c.Mode = "bridge"
	vlan, e := strconv.Atoi(strings.TrimSpace(text(a.controls[vlanEdit])))
	if e != nil || vlan < 0 || vlan > 4094 { return c, fmt.Errorf("VLAN ID: 0 (без тега) или 1–4094") }
	if vlan != c.VLANID { return c, fmt.Errorf("для изменения VLAN нажмите «Создать / VLAN…» и подтвердите настройку") }
	ui := int(send(a.controls[uplinkCombo], 0x147, 0, 0))
	if ui >= 0 && ui < len(a.uplinks) { c.Uplink = a.uplinks[ui].Name }
	c.RawFormat = "text/plain"
	if a.checked(rawCheck) {
		c.RawFormat = "application/octet-stream"
	}
	i := int(send(a.controls[netCombo], 0x147, 0, 0))
	if i < 0 || i >= len(a.adapters) {
		return c, fmt.Errorf("сначала создайте виртуальный адаптер LANPrint")
	}
	c.Interface = a.adapters[i].Name
	c.Address = a.adapters[i].Address
	i = int(send(a.controls[printerCombo], 0x147, 0, 0))
	if i < 0 || i >= len(a.printers) {
		return c, fmt.Errorf("выберите принтер")
	}
	c.Printer = a.printers[i].Name
	ni := int(send(a.controls[netCombo], 0x147, 0, 0))
	if !a.adapters[ni].Preferred || !a.adapters[ni].Up || strings.HasPrefix(c.Address, "169.254.") || c.Address == "" {
		check := c; check.Address = "192.0.2.1"
		if e := check.Validate(); e != nil { return c, e }
		return c, errVirtualIP
	}
	return c, c.Validate()
}
func (a *application) refresh() {
	a.async(func() result {
		ad, e := platform.Adapters()
		p, pe := platform.Printers()
		var issues []string
		if e != nil {
			issues = append(issues, "Сетевые адаптеры: "+e.Error())
		}
		if pe != nil {
			issues = append(issues, "Принтеры: "+pe.Error())
		}
		var combined error
		if len(issues) > 0 {
			combined = fmt.Errorf("%s", strings.Join(issues, "\n"))
		}
		return result{err: combined, fn: func() {
			if e == nil {
				a.uplinks, a.adapters = platform.SplitAdapters(ad)
			}
			if pe == nil {
				a.printers = p
			}
			a.populate()
		}}
	})
}
func (a *application) createBridge(startAfter bool) {
	if a.busy { return }
	if a.server != nil { a.error(fmt.Errorf("остановите службы перед настройкой виртуального адаптера")); return }
	i := int(send(a.controls[uplinkCombo], 0x147, 0, 0))
	if i < 0 || i >= len(a.uplinks) { a.error(fmt.Errorf("выберите физический Ethernet для подключения к LAN")); return }
	uplink := a.uplinks[i].Name
	vlan, e := strconv.Atoi(strings.TrimSpace(text(a.controls[vlanEdit])))
	if e != nil || vlan < 0 || vlan > 4094 { a.error(fmt.Errorf("VLAN ID: 0 (без тега) или 1–4094")); return }
	if !a.confirm(fmt.Sprintf("Создать или настроить выделенный виртуальный адаптер LANPrint через «%s», VLAN ID %d (0 = без тега)?\nПриложение при необходимости включит Hyper-V, создаст внешний коммутатор и отдельную виртуальную карту. DHCP запросит адрес из выбранной VLAN. На Ethernet порту должна быть разрешена эта VLAN.\nСеть может кратковременно прерваться; потребуется UAC. Если Hyper-V не включён, потребуется перезагрузка Windows. Продолжить?",uplink,vlan)) { return }
	startAfter = startAfter || a.desired
	a.desired = false
	a.cfg.Uplink = uplink
	pi := int(send(a.controls[printerCombo], 0x147, 0, 0))
	if pi >= 0 && pi < len(a.printers) { a.cfg.Printer = a.printers[pi].Name }
	set(a.controls[statusEdit], "Создание виртуального адаптера / ожидание DHCP…")
	a.async(func() result {
		r, e := platform.CreateBridge(uplink,vlan)
		ad, ae := platform.Adapters()
		if e == nil { e = ae }
		return result{err: e, fn: func() {
			if ae == nil { a.uplinks, a.adapters = platform.SplitAdapters(ad) }
			a.cfg.Mode = "bridge"
			a.cfg.Interface = core.VirtualAdapter
			if r.OK { a.cfg.Address = r.Address; a.cfg.VLANID = r.VLANID; a.log(r.Message) }
			a.populate()
			if r.OK && ae == nil {
				set(a.controls[statusEdit], r.Message)
				if e := core.Save(a.path, a.cfg); e != nil { a.error(e) }
				if startAfter { a.start() }
			} else { set(a.controls[statusEdit], "Настройка не завершена; см. сообщение и журнал") }
		}}
	})
}
func (a *application) start() {
	if len(a.adapters) == 0 { a.createBridge(true); return }
	c, e := a.read()
	if e != nil && e != errVirtualIP {
		a.error(e)
		return
	}
	if c.RawFormat == "application/octet-stream" && !a.confirm("RAW passthrough не преобразует PDF, Office или изображения. Клиенты должны использовать язык вашего принтера (PCL/PostScript и т.п.). Включить?") {
		return
	}
	a.cfg = c
	a.desired = true
	waiting := e == errVirtualIP
	a.async(func() result {
		if a.server != nil {
			a.server.Stop()
		}
		var s *core.Server
		var e error
		if !waiting { s, e = core.Start(c, platform.Backend{}, a.log) }
		if e == nil {
			e = core.Save(a.path, c)
		}
		return result{err: e, fn: func() {
			a.server = s
			if waiting { set(a.controls[statusEdit], errVirtualIP.Error()) }
			if s == nil && !waiting {
				a.desired = false
			}
		}}
	})
}
func (a *application) stop() {
	a.desired = false
	a.async(func() result {
		if a.server != nil {
			a.server.Stop()
		}
		return result{fn: func() { a.server = nil }}
	})
}
func (a *application) monitor() {
	if !a.desired || a.busy {
		return
	}
	c := a.cfg
	old := a.server
	a.async(func() result {
		ad, e := platform.Adapters()
		if e != nil {
			return result{err: e}
		}
		address := ""
		for _, v := range ad {
			if v.Name == core.VirtualAdapter && !v.Physical && v.Preferred && v.Up && v.Address != "" && !strings.HasPrefix(v.Address, "169.254.") {
				if address == "" || v.Address == c.Address {
					address = v.Address
				}
			}
		}
		if old != nil && address == c.Address {
			status, e := platform.Backend{}.Status(c.Printer)
			return result{fn: func() {
				if e != nil {
					status = e.Error()
				}
				set(a.controls[statusEdit], "Работает: "+c.Address+" | "+status)
			}}
		}
		if old != nil {
			old.Stop()
		}
		var s *core.Server
		if address != "" {
			c.Address = address
			s, e = core.Start(c, platform.Backend{}, a.log)
		}
		return result{err: e, fn: func() {
			a.server = s
			a.cfg = c
			if s != nil {
				if e := core.Save(a.path, c); e != nil {
					a.log(e.Error())
				}
				a.uplinks, a.adapters = platform.SplitAdapters(ad)
				a.populate()
				set(a.controls[statusEdit], "Соединение восстановлено: "+c.Address)
			} else {
				set(a.controls[statusEdit], "Ожидание выбранного интерфейса")
			}
		}}
	})
}
func (a *application) network(mode string) {
	if a.busy || a.server != nil || a.desired {
		a.error(fmt.Errorf("сначала остановите службы"))
		return
	}
	i := int(send(a.controls[netCombo], 0x147, 0, 0))
	if i < 0 || i >= len(a.adapters) {
		a.error(fmt.Errorf("сначала создайте виртуальный адаптер LANPrint"))
		return
	}
	adapter := a.adapters[i]
	ip, mask, gw, dns := text(a.controls[ipEdit]), text(a.controls[maskEdit]), text(a.controls[gwEdit]), text(a.controls[dnsEdit])
	if mode == "static" {
		if e := core.ValidateNetwork(ip, mask, gw, dns); e != nil {
			a.error(e)
			return
		}
	}
	if !a.confirm("Изменить IPv4 выбранного адаптера " + adapter.Name + "?\nСеть этого адаптера будет прервана. Для Static IP подтвердите отсутствие конфликта в DHCP/IPAM вашего роутера. Потребуется UAC. После завершения нажмите «Обновить».") {
		return
	}
	a.error(platform.ApplyNetwork(adapter.Name, mode, ip, mask, gw, dns))
}
func (a *application) command(id int) {
	if a.busy {
		return
	}
	switch id {
	case netCombo:
		a.selectAdapter()
	case 201:
		a.refresh()
	case 202:
		a.start()
	case 203:
		a.stop()
	case 204:
		if a.server != nil || a.desired {
			a.error(fmt.Errorf("Остановите службы перед сохранением настроек"))
			return
		}
		c, e := a.read()
		if e == errVirtualIP { e = nil }
		if e == nil {
			exe, _ := os.Executable()
			e = platform.AutoRun(exe, c.AutoStart)
		}
		if e == nil {
			e = core.Save(a.path, c)
			a.cfg = c
		}
		a.error(e)
	case 205:
		if a.server == nil {
			a.error(fmt.Errorf("запустите службы"))
			return
		}
		_, e := a.server.Queue.Submit("Тестовая страница", "local GUI", "text/plain", []byte("LANPrint "+core.Version+"\nТестовая страница\n"+time.Now().Format(time.RFC3339)+"\n"))
		a.error(e)
	case 206:
		if a.server == nil {
			return
		}
		a.error(platform.OpenURL(fmt.Sprintf("http://%s:%d/", a.server.Cfg.Address, a.cfg.AdminPort)))
	case 207:
		c, e := a.read()
		if e != nil {
			a.error(e)
			return
		}
		a.async(func() result {
			s, e := platform.OSJobs(c.Printer)
			return result{err: e, fn: func() { a.log("Windows spooler queue:\n" + s) }}
		})
	case 208:
		if a.server == nil {
			return
		}
		var id int32
		if _, e := fmt.Sscan(text(a.controls[jobEdit]), &id); e != nil {
			a.error(e)
			return
		}
		a.error(a.server.Queue.Cancel(id))
	case 209:
		a.network("dhcp")
	case 210:
		a.network("static")
	case 211:
		if a.confirm("Завершить LANPrint и остановить все службы?") {
			user.NewProc("DestroyWindow").Call(a.hwnd)
		}
	case 212:
		exe, _ := os.Executable()
		a.error(platform.OpenURL(strings.TrimSuffix(exe, "LANPrint.exe") + "docs\\INSTALLATION.md"))
	case 213:
		a.createBridge(false)
	}
}
func windowProc(h uintptr, m uint32, w, l uintptr) uintptr {
	a := app
	switch m {
	case 0x111:
		id := int(w & 0xffff)
		if id == netCombo && w>>16 != 1 {
			return 0
		}
		a.command(id)
		return 0
	case 0x113:
		a.tick++
		for {
			select {
			case r := <-a.results:
				a.busy = false
				if r.fn != nil {
					r.fn()
				}
				a.error(r.err)
			default:
				goto drained
			}
		}
	drained:
		for i := 0; i < 100; i++ {
			select {
			case s := <-a.logs:
				a.lines = append(a.lines, s)
				if len(a.lines) > 150 {
					a.lines = a.lines[len(a.lines)-150:]
				}
				set(a.controls[logEdit], strings.Join(a.lines, "\r\n"))
				send(a.controls[logEdit], 0xb1, ^uintptr(0), ^uintptr(0))
				send(a.controls[logEdit], 0xb7, 0, 0)
			default:
				goto logsDone
			}
		}
	logsDone:
		if a.tick%10 == 0 {
			a.monitor()
		}
		if a.server == nil && !a.desired {
			set(a.controls[statusEdit], "Службы остановлены")
		}
		return 0
	case 0x10:
		user.NewProc("ShowWindow").Call(h, 0)
		return 0
	case 0x8001:
		if l == 0x202 || l == 0x205 || l == 0x203 {
			user.NewProc("ShowWindow").Call(h, 9)
			user.NewProc("SetForegroundWindow").Call(h)
		}
		return 0
	case 2:
		if a.server != nil {
			a.server.Stop()
		}
		shell.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&a.icon)))
		user.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	r, _, _ := user.NewProc("DefWindowProcW").Call(h, uintptr(m), w, l)
	return r
}
func Run(c core.Config, path string, log func(string), logs <-chan string, autostart bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	a := &application{cfg: c, path: path, log: log, logs: logs, controls: map[int]uintptr{}, results: make(chan result, 4)}
	app = a
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	icon, _, _ := user.NewProc("LoadIconW").Call(0, 32516)
	wc := windowClass{Proc: syscall.NewCallback(windowProc), Instance: instance, Cursor: cursor, Icon: icon, Background: 16, Name: u("LANPrintWindow")}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, e := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return e
	}
	h, _, e := user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(wc.Name)), uintptr(unsafe.Pointer(u("LANPrint "+core.Version))), 0x00CA0000, 100, 60, 1040, 840, 0, 0, instance, 0)
	if h == 0 {
		return e
	}
	a.hwnd = h
	a.label("Подключение к LAN — физический Ethernet (только uplink)", 20, 15, 900)
	a.control("COMBOBOX", "", uplinkCombo, 20, 40, 410, 250, 0x00200003)
	a.button("Создать / VLAN…", 213, 450, 40, 225)
	a.button("Обновить", 201, 695, 40, 145)
	a.label("VLAN ID", 860, 15, 120)
	a.edit(strconv.Itoa(c.VLANID), vlanEdit, 860, 40, 110)
	a.label("0 = без тега", 860, 78, 150)
	a.label("Адаптер принтера", 20, 78, 180)
	a.control("COMBOBOX", "", netCombo, 200, 75, 640, 250, 0x00200003)
	a.label("Принтер работает только на отдельном vEthernet (LANPrint-Printer), с собственной MAC и IPv4.", 20, 103, 970)
	a.label("", 20, 128, 900)
	a.controls[netInfo] = a.controls[0]
	a.label("IPv4", 20, 158, 160)
	a.label("Маска", 210, 158, 160)
	a.label("Шлюз", 400, 158, 160)
	a.label("DNS (через запятую)", 590, 158, 260)
	a.edit(c.Address, ipEdit, 20, 183, 170)
	a.edit("255.255.255.0", maskEdit, 210, 183, 170)
	a.edit("", gwEdit, 400, 183, 170)
	a.edit("", dnsEdit, 590, 183, 290)
	a.button("Применить DHCP…", 209, 20, 218, 190)
	a.button("Применить Static…", 210, 225, 218, 190)
	a.button("Инструкция Bridge", 212, 430, 218, 190)
	a.label("Физический принтер Windows", 20, 260, 450)
	a.control("COMBOBOX", "", printerCombo, 20, 285, 640, 250, 0x00200003)
	a.button("Очередь Windows", 207, 680, 285, 180)
	a.label("Имя в сети", 20, 320, 170)
	a.edit(c.Name, nameEdit, 200, 318, 400)
	a.check("RAW passthrough (только совместимый PDL)", rawCheck, 20, 354, 570, c.RawFormat == "application/octet-stream")
	a.check("Запуск при входе в Windows", autoCheck, 610, 354, 350, c.AutoStart)
	a.button("Запустить", 202, 20, 391, 135)
	a.button("Остановить", 203, 170, 391, 135)
	a.button("Сохранить", 204, 320, 391, 135)
	a.button("Тестовая печать", 205, 470, 391, 155)
	a.button("HTTP управление", 206, 640, 391, 185)
	a.button("Выход", 211, 840, 391, 140)
	a.label("Службы остановлены", 20, 428, 960)
	a.controls[statusEdit] = a.controls[0]
	a.label("Токен HTTP / отмены IPP (хранится локально в config.json)", 20, 459, 950)
	a.edit(c.Token, tokenEdit, 20, 484, 960)
	a.label("ID LANPrint", 20, 523, 120)
	a.edit("", jobEdit, 150, 519, 120)
	a.button("Отменить задание", 208, 290, 518, 200)
	a.label("Журнал: received → processing → spooled (физическая печать зависит от драйвера)", 20, 559, 960)
	a.control("EDIT", "", logEdit, 20, 585, 960, 195, 0x00a00844)
	a.icon = notifyIcon{Hwnd: h, ID: 1, Flags: 7, Callback: 0x8001, Icon: icon}
	a.icon.Size = uint32(unsafe.Sizeof(a.icon))
	copy(a.icon.Tip[:], syscall.StringToUTF16("LANPrint — нажмите для открытия"))
	shell.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&a.icon)))
	user.NewProc("SetTimer").Call(h, 1, 500, 0)
	user.NewProc("ShowWindow").Call(h, 1)
	a.refresh()
	if autostart && c.AutoStart {
		a.desired = true
		a.cfg = c
	}
	var msg message
	for {
		r, _, _ := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}
