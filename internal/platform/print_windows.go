//go:build windows

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"syscall"
	"unsafe"
)

var spool = syscall.NewLazyDLL("winspool.drv")
var gdi = syscall.NewLazyDLL("gdi32.dll")

type Backend struct{}

func ptr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

//go:uintptrescapes
func call(d *syscall.LazyDLL, n string, args ...uintptr) (uintptr, error) {
	r, _, e := d.NewProc(n).Call(args...)
	if r == 0 || ((n == "StartPage" || n == "EndPage") && int32(r) < 0) {
		return 0, fmt.Errorf("%s: %v", n, e)
	}
	return r, nil
}
func openPrinter(name string) (uintptr, error) {
	var h uintptr
	_, e := call(spool, "OpenPrinterW", uintptr(unsafe.Pointer(ptr(name))), uintptr(unsafe.Pointer(&h)), 0)
	return h, e
}
func (Backend) Status(name string) (string, error) {
	h, e := openPrinter(name)
	if e != nil {
		return "", e
	}
	defer spool.NewProc("ClosePrinter").Call(h)
	var status, needed uint32
	_, e = call(spool, "GetPrinterW", h, 6, uintptr(unsafe.Pointer(&status)), 4, uintptr(unsafe.Pointer(&needed)))
	if e != nil {
		return "", e
	}
	if status == 0 {
		return "ready (driver reported)", nil
	}
	return fmt.Sprintf("Windows printer status flags: 0x%08x", status), nil
}
func (Backend) Cancel(name string, id uint32) error {
	h, e := openPrinter(name)
	if e != nil {
		return e
	}
	defer spool.NewProc("ClosePrinter").Call(h)
	_, e = call(spool, "SetJobW", h, uintptr(id), 0, 0, 3)
	return e
}
func (Backend) Print(ctx context.Context, printer, format string, data []byte) (uint32, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if format == "application/octet-stream" {
		return printRaw(ctx, printer, data)
	}
	return printGDI(ctx, printer, format, data)
}

type docRaw struct{ Name, Output, Datatype *uint16 }

func printRaw(ctx context.Context, printer string, data []byte) (uint32, error) {
	h, e := openPrinter(printer)
	if e != nil {
		return 0, e
	}
	defer spool.NewProc("ClosePrinter").Call(h)
	doc := docRaw{ptr("LANPrint RAW"), nil, ptr("RAW")}
	id, e := call(spool, "StartDocPrinterW", h, 1, uintptr(unsafe.Pointer(&doc)))
	if e != nil {
		return 0, e
	}
	success := false
	defer func() {
		if !success {
			spool.NewProc("AbortPrinter").Call(h)
		}
	}()
	if _, e = call(spool, "StartPagePrinter", h); e != nil {
		return uint32(id), e
	}
	for offset := 0; offset < len(data); {
		if e = ctx.Err(); e != nil {
			return uint32(id), e
		}
		n := len(data) - offset
		if n > 65536 {
			n = 65536
		}
		var wrote uint32
		if _, e = call(spool, "WritePrinter", h, uintptr(unsafe.Pointer(&data[offset])), uintptr(n), uintptr(unsafe.Pointer(&wrote))); e != nil {
			return uint32(id), e
		}
		if wrote == 0 {
			return uint32(id), errors.New("spooler wrote zero bytes")
		}
		offset += int(wrote)
	}
	if _, e = call(spool, "EndPagePrinter", h); e != nil {
		return uint32(id), e
	}
	if _, e = call(spool, "EndDocPrinter", h); e != nil {
		return uint32(id), e
	}
	success = true
	return uint32(id), nil
}

type docInfo struct {
	Size                   int32
	Name, Output, Datatype *uint16
	Type                   uint32
}

func printGDI(ctx context.Context, printer, format string, data []byte) (uint32, error) {
	dc, e := call(gdi, "CreateDCW", uintptr(unsafe.Pointer(ptr("WINSPOOL"))), uintptr(unsafe.Pointer(ptr(printer))), 0, 0)
	if e != nil {
		return 0, e
	}
	defer gdi.NewProc("DeleteDC").Call(dc)
	doc := docInfo{Name: ptr("LANPrint document")}
	doc.Size = int32(unsafe.Sizeof(doc))
	id, _, last := gdi.NewProc("StartDocW").Call(dc, uintptr(unsafe.Pointer(&doc)))
	if int32(id) <= 0 {
		return 0, fmt.Errorf("StartDoc: %v", last)
	}
	success := false
	defer func() {
		if !success {
			gdi.NewProc("AbortDoc").Call(dc)
		}
	}()
	caps := func(n int) int { v, _, _ := gdi.NewProc("GetDeviceCaps").Call(dc, uintptr(n)); return int(v) }
	width, height, dpi := caps(8), caps(10), caps(90)
	margin := dpi / 3
	if margin < 10 {
		margin = 10
	}
	if width <= 2*margin || height <= 2*margin {
		return uint32(id), errors.New("invalid printable page")
	}
	if _, e = call(gdi, "StartPage", dc); e != nil {
		return uint32(id), e
	}
	if format == "text/plain" {
		font, e := call(gdi, "CreateFontW", uintptr(int32(-dpi*10/72)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 49, uintptr(unsafe.Pointer(ptr("Courier New"))))
		if e != nil {
			return uint32(id), e
		}
		defer gdi.NewProc("DeleteObject").Call(font)
		old, _, _ := gdi.NewProc("SelectObject").Call(dc, font)
		defer gdi.NewProc("SelectObject").Call(dc, old)
		var size struct{ X, Y int32 }
		if _, e = call(gdi, "GetTextExtentPoint32W", dc, uintptr(unsafe.Pointer(ptr("M"))), 1, uintptr(unsafe.Pointer(&size))); e != nil {
			return uint32(id), e
		}
		if size.X <= 0 || size.Y <= 0 {
			return uint32(id), errors.New("invalid font metrics")
		}
		columns := (width - 2*margin) / int(size.X)
		if columns < 1 {
			return uint32(id), errors.New("page too narrow")
		}
		lineHeight := int(size.Y) * 12 / 10
		y := margin
		newPage := func() error {
			if _, e := call(gdi, "EndPage", dc); e != nil {
				return e
			}
			if _, e := call(gdi, "StartPage", dc); e != nil {
				return e
			}
			y = margin
			return nil
		}
		text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\t", "    ")
		pages := strings.Split(text, "\f")
		for pi, page := range pages {
			if pi > 0 {
				if e = newPage(); e != nil {
					return uint32(id), e
				}
			}
			for _, line := range strings.Split(page, "\n") {
				runes := []rune(line)
				for {
					if e = ctx.Err(); e != nil {
						return uint32(id), e
					}
					if y+lineHeight > height-margin {
						if e = newPage(); e != nil {
							return uint32(id), e
						}
					}
					n := len(runes)
					if n > columns {
						n = columns
					}
					u, e := syscall.UTF16FromString(string(runes[:n]))
					if e != nil {
						return uint32(id), e
					}
					if n > 0 {
						if _, e = call(gdi, "TextOutW", dc, uintptr(margin), uintptr(y), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1)); e != nil {
							return uint32(id), e
						}
					}
					y += lineHeight
					runes = runes[n:]
					if len(runes) == 0 {
						break
					}
				}
			}
		}
	} else {
		img, _, e := image.Decode(bytes.NewReader(data))
		if e != nil {
			return uint32(id), e
		}
		rect := img.Bounds()
		w, h := rect.Dx(), rect.Dy()
		pixels := make([]byte, w*h*4)
		for y := 0; y < h; y++ {
			if e = ctx.Err(); e != nil {
				return uint32(id), e
			}
			for x := 0; x < w; x++ {
				r, g, b, _ := img.At(rect.Min.X+x, rect.Min.Y+y).RGBA()
				i := ((h-1-y)*w + x) * 4
				pixels[i] = byte(b >> 8)
				pixels[i+1] = byte(g >> 8)
				pixels[i+2] = byte(r >> 8)
			}
		}
		header := struct {
			Size                   uint32
			Width, Height          int32
			Planes, BitCount       uint16
			Compression, SizeImage uint32
			XPels, YPels           int32
			ClrUsed, ClrImportant  uint32
		}{Size: 40, Width: int32(w), Height: int32(h), Planes: 1, BitCount: 32}
		dw, dh := width-2*margin, height-2*margin
		if int64(dw)*int64(h) < int64(dh)*int64(w) {
			dh = dw * h / w
		} else {
			dw = dh * w / h
		}
		result, _, last := gdi.NewProc("StretchDIBits").Call(dc, uintptr(margin), uintptr(margin), uintptr(dw), uintptr(dh), 0, 0, uintptr(w), uintptr(h), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&header)), 0, 0x00CC0020)
		if result == 0 || int32(result) == -1 {
			return uint32(id), fmt.Errorf("StretchDIBits: %v", last)
		}
	}
	if e = ctx.Err(); e != nil {
		return uint32(id), e
	}
	if _, e = call(gdi, "EndPage", dc); e != nil {
		return uint32(id), e
	}
	result, _, last := gdi.NewProc("EndDoc").Call(dc)
	if int32(result) <= 0 {
		return uint32(id), fmt.Errorf("EndDoc: %v", last)
	}
	success = true
	return uint32(id), nil
}
