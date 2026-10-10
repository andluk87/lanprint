package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"sync"
	"time"
	"unicode/utf8"
)

type Backend interface {
	Print(context.Context, string, string, []byte) (uint32, error)
	Cancel(string, uint32) error
	Status(string) (string, error)
}
type Job struct {
	ID      int32     `json:"id"`
	Name    string    `json:"name"`
	Client  string    `json:"client"`
	Format  string    `json:"format"`
	State   string    `json:"state"`
	Error   string    `json:"error,omitempty"`
	Created time.Time `json:"created"`
	SpoolID uint32    `json:"spool_id,omitempty"`
	data    []byte
	cancel  context.CancelFunc
}
type Queue struct {
	mu      sync.Mutex
	jobs    []*Job
	pending chan *Job
	backend Backend
	cfg     Config
	next    int32
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	Log     func(string)
}

func NewQueue(c Config, b Backend, log func(string)) *Queue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{cfg: c, backend: b, pending: make(chan *Job, c.MaxQueued), ctx: ctx, cancel: cancel, done: make(chan struct{}), Log: log}
	go q.run()
	return q
}
func ValidateDocument(format string, data []byte) error {
	if len(data) == 0 {
		return errors.New("пустое задание")
	}
	switch format {
	case "text/plain":
		if !utf8.Valid(data) || bytes.Contains(data, []byte{0}) {
			return errors.New("ожидается текст UTF-8")
		}
	case "image/jpeg", "image/png":
		c, f, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil {
			return e
		}
		if (format == "image/jpeg" && f != "jpeg") || (format == "image/png" && f != "png") {
			return errors.New("формат изображения не соответствует MIME")
		}
		if c.Width < 1 || c.Height < 1 || int64(c.Width)*int64(c.Height) > 20_000_000 {
			return errors.New("изображение превышает 20 мегапикселей")
		}
	case "application/octet-stream":
	default:
		return fmt.Errorf("формат %s не поддерживается", format)
	}
	return nil
}
func (q *Queue) Submit(name, client, format string, data []byte) (Job, error) {
	if int64(len(data)) > q.cfg.MaxJobBytes {
		return Job{}, errors.New("задание превышает лимит")
	}
	if format == "application/octet-stream" && q.cfg.RawFormat != "application/octet-stream" {
		return Job{}, errors.New("RAW passthrough не включён")
	}
	if e := ValidateDocument(format, data); e != nil {
		return Job{}, e
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ctx.Err() != nil {
		return Job{}, errors.New("служба остановлена")
	}
	q.next++
	j := &Job{ID: q.next, Name: name, Client: client, Format: format, State: "pending", Created: time.Now(), data: append([]byte(nil), data...)}
	select {
	case q.pending <- j:
		q.jobs = append(q.jobs, j)
		if len(q.jobs) > 256 {
			q.jobs = q.jobs[len(q.jobs)-256:]
		}
		q.Log(fmt.Sprintf("job=%d client=%s name=%q received", j.ID, client, name))
		copy := *j
		copy.data = nil
		return copy, nil
	default:
		return Job{}, errors.New("очередь заполнена")
	}
}
func (q *Queue) run() {
	defer close(q.done)
	for {
		select {
		case <-q.ctx.Done():
			q.mu.Lock()
			for _, j := range q.jobs {
				if j.State == "pending" {
					j.State = "canceled"
					j.data = nil
				}
			}
			q.mu.Unlock()
			return
		case j := <-q.pending:
			q.mu.Lock()
			if j.State == "canceled" {
				q.mu.Unlock()
				continue
			}
			ctx, cancel := context.WithCancel(q.ctx)
			j.cancel = cancel
			j.State = "processing"
			data := j.data
			q.mu.Unlock()
			id, e := q.backend.Print(ctx, q.cfg.Printer, j.Format, data)
			cancel()
			q.mu.Lock()
			j.data = nil
			j.cancel = nil
			j.SpoolID = id
			if j.State == "canceled" && id != 0 && e == nil {
				if ce := q.backend.Cancel(q.cfg.Printer, id); ce != nil {
					j.Error = ce.Error()
				}
			}
			if j.State != "canceled" {
				if e != nil {
					j.State = "aborted"
					j.Error = e.Error()
				} else {
					j.State = "spooled"
				}
			}
			state, errText := j.State, j.Error
			q.mu.Unlock()
			q.Log(fmt.Sprintf("job=%d state=%s spool=%d error=%q", j.ID, state, id, errText))
		}
	}
}
func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		v := *j
		v.data = nil
		v.cancel = nil
		out = append(out, v)
	}
	return out
}
func (q *Queue) Get(id int32) (Job, bool) {
	for _, j := range q.List() {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}
func (q *Queue) Cancel(id int32) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.ID == id {
			if j.State == "aborted" || j.State == "canceled" {
				return errors.New("задание уже завершено")
			}
			if j.SpoolID != 0 {
				if e := q.backend.Cancel(q.cfg.Printer, j.SpoolID); e != nil {
					return e
				}
			}
			j.State = "canceled"
			j.data = nil
			if j.cancel != nil {
				j.cancel()
			}
			return nil
		}
	}
	return errors.New("задание не найдено")
}
func (q *Queue) Stop() { q.cancel(); <-q.done }
