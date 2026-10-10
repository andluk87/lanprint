package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type Attr struct {
	Tag   byte
	Name  string
	Value []byte
}
type IPP struct {
	Major, Minor byte
	Code         uint16
	ID           uint32
	Attrs        []Attr
	Document     []byte
}

func DecodeIPP(b []byte) (IPP, error) {
	p := IPP{}
	if len(b) < 9 {
		return p, errors.New("short IPP")
	}
	p.Major = b[0]
	p.Minor = b[1]
	p.Code = binary.BigEndian.Uint16(b[2:])
	p.ID = binary.BigEndian.Uint32(b[4:])
	if p.Major != 1 && p.Major != 2 {
		return p, errors.New("IPP version")
	}
	i := 8
	last := ""
	group := byte(0)
	for i < len(b) {
		tag := b[i]
		i++
		if tag == 3 {
			p.Document = b[i:]
			return p, nil
		}
		if tag < 0x10 {
			if tag != 1 && tag != 2 && tag != 4 && tag != 5 {
				return p, errors.New("invalid group")
			}
			group = tag
			last = ""
			continue
		}
		if group == 0 || i+2 > len(b) {
			return p, errors.New("invalid attribute")
		}
		n := int(binary.BigEndian.Uint16(b[i:]))
		i += 2
		if i+n+2 > len(b) {
			return p, errors.New("truncated name")
		}
		name := string(b[i : i+n])
		i += n
		if n == 0 {
			name = last
		}
		if name == "" {
			return p, errors.New("empty name")
		}
		last = name
		n = int(binary.BigEndian.Uint16(b[i:]))
		i += 2
		if i+n > len(b) {
			return p, errors.New("truncated value")
		}
		p.Attrs = append(p.Attrs, Attr{tag, name, b[i : i+n]})
		if len(p.Attrs) > 256 {
			return p, errors.New("too many attributes")
		}
		i += n
	}
	return p, errors.New("missing end tag")
}
func (p IPP) Value(name string) string {
	for _, a := range p.Attrs {
		if a.Name == name {
			return string(a.Value)
		}
	}
	return ""
}
func (p IPP) Number(name string) int32 {
	for _, a := range p.Attrs {
		if a.Name == name && len(a.Value) == 4 {
			return int32(binary.BigEndian.Uint32(a.Value))
		}
	}
	return 0
}

type ippWriter struct{ bytes.Buffer }

func (w *ippWriter) attr(tag byte, name string, value []byte) {
	w.WriteByte(tag)
	binary.Write(&w.Buffer, binary.BigEndian, uint16(len(name)))
	w.WriteString(name)
	binary.Write(&w.Buffer, binary.BigEndian, uint16(len(value)))
	w.Write(value)
}
func (w *ippWriter) str(tag byte, name, value string) { w.attr(tag, name, []byte(value)) }
func (w *ippWriter) num(tag byte, name string, value int32) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(value))
	w.attr(tag, name, b)
}
func (w *ippWriter) job(c Config, j Job) {
	w.WriteByte(2)
	w.num(0x21, "job-id", j.ID)
	w.str(0x45, "job-uri", fmt.Sprintf("ipp://%s:%d/jobs/%d", c.Address, c.IPPPort, j.ID))
	w.str(0x42, "job-name", j.Name)
	state := int32(3)
	reason := "job-incoming"
	switch j.State {
	case "processing":
		state = 5
		reason = "job-printing"
	case "spooled":
		state = 5
		reason = "job-queued-for-marker"
	case "canceled":
		state = 7
		reason = "job-canceled-by-user"
	case "aborted":
		state = 8
		reason = "aborted-by-system"
	}
	w.num(0x23, "job-state", state)
	w.str(0x44, "job-state-reasons", reason)
}
func IPPHandler(c Config, q *Queue, b Backend) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/ipp/print" {
			http.NotFound(rw, r)
			return
		}
		if !Allowed(c, r.RemoteAddr) {
			http.Error(rw, "forbidden", 403)
			return
		}
		if r.Header.Get("Content-Type") != "application/ipp" {
			http.Error(rw, "application/ipp required", 415)
			return
		}
		data, e := io.ReadAll(http.MaxBytesReader(rw, r.Body, c.MaxJobBytes+65536))
		if e != nil {
			http.Error(rw, "request too large", 413)
			return
		}
		p, e := DecodeIPP(data)
		if e != nil {
			http.Error(rw, e.Error(), 400)
			return
		}
		w := &ippWriter{}
		w.Write([]byte{p.Major, p.Minor, 0, 0})
		binary.Write(&w.Buffer, binary.BigEndian, p.ID)
		w.WriteByte(1)
		w.str(0x47, "attributes-charset", "utf-8")
		w.str(0x48, "attributes-natural-language", "en")
		status := uint16(0)
		format := p.Value("document-format")
		if format == "" {
			format = "text/plain"
		}
		valid := format == "text/plain" || format == "image/jpeg" || format == "image/png" || (format == "application/octet-stream" && c.RawFormat == format)
		switch p.Code {
		case 2, 4:
			if !valid {
				status = 0x040a
				break
			}
			if p.Value("compression") != "" && p.Value("compression") != "none" {
				status = 0x040f
				break
			}
			if p.Number("copies") > 1 {
				status = 0x040b
				break
			}
			if p.Code == 4 {
				break
			}
			name := p.Value("job-name")
			if name == "" {
				name = "IPP document"
			}
			j, e := q.Submit(name, r.RemoteAddr, format, p.Document)
			if e != nil {
				status = 0x0500
				w.str(0x41, "status-message", e.Error())
			} else {
				w.job(c, j)
			}
		case 0x0b:
			w.WriteByte(4)
			w.str(0x42, "printer-name", c.Name)
			w.str(0x41, "printer-info", c.Name)
			w.str(0x45, "printer-uri-supported", fmt.Sprintf("ipp://%s:%d/ipp/print", c.Address, c.IPPPort))
			w.str(0x44, "uri-security-supported", "none")
			w.str(0x44, "uri-authentication-supported", "none")
			w.str(0x47, "charset-configured", "utf-8")
			w.str(0x47, "charset-supported", "utf-8")
			w.str(0x48, "natural-language-configured", "en")
			w.str(0x48, "generated-natural-language-supported", "en")
			w.str(0x44, "ipp-versions-supported", "1.1")
			w.str(0x44, "", "2.0")
			w.str(0x44, "compression-supported", "none")
			w.attr(0x22, "printer-is-accepting-jobs", []byte{1})
			w.num(0x23, "printer-state", 3)
			if s, e := b.Status(c.Printer); e != nil {
				w.str(0x44, "printer-state-reasons", "other")
				w.str(0x41, "printer-state-message", e.Error())
			} else {
				w.str(0x44, "printer-state-reasons", "none")
				w.str(0x41, "printer-state-message", s)
			}
			w.str(0x49, "document-format-default", "text/plain")
			w.str(0x49, "document-format-supported", "text/plain")
			w.str(0x49, "", "image/jpeg")
			w.str(0x49, "", "image/png")
			if c.RawFormat == "application/octet-stream" {
				w.str(0x49, "", "application/octet-stream")
			}
			for i, op := range []int32{2, 4, 8, 9, 10, 11} {
				name := ""
				if i == 0 {
					name = "operations-supported"
				}
				w.num(0x23, name, op)
			}
			pending := int32(0)
			for _, j := range q.List() {
				if j.State == "pending" || j.State == "processing" {
					pending++
				}
			}
			w.num(0x21, "queued-job-count", pending)
		case 9:
			id := p.Number("job-id")
			j, ok := q.Get(id)
			if !ok {
				status = 0x0406
			} else {
				w.job(c, j)
			}
		case 10:
			for _, j := range q.List() {
				w.job(c, j)
			}
		case 8:
			if !authorized(c, r) {
				status = 0x0403
				break
			}
			if e := q.Cancel(p.Number("job-id")); e != nil {
				status = 0x0406
			}
		default:
			status = 0x0501
		}
		binary.BigEndian.PutUint16(w.Bytes()[2:4], status)
		w.WriteByte(3)
		rw.Header().Set("Content-Type", "application/ipp")
		rw.Write(w.Bytes())
	}
}
func authorized(c Config, r *http.Request) bool {
	token := r.Header.Get("Authorization")
	return secureEqual(token, "Bearer "+c.Token)
}
func parseJobID(s string) (int32, error) { v, e := strconv.ParseInt(s, 10, 32); return int32(v), e }
