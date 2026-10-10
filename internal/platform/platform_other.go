//go:build !windows

package platform

import (
	"context"
	"errors"
)

type Backend struct{}

func Adapters() ([]Adapter, error) { return nil, errors.New("Windows required") }
func Printers() ([]Printer, error) { return nil, errors.New("Windows required") }
func (Backend) Print(context.Context, string, string, []byte) (uint32, error) {
	return 0, errors.New("Windows required")
}
func (Backend) Cancel(string, uint32) error   { return errors.New("Windows required") }
func (Backend) Status(string) (string, error) { return "", errors.New("Windows required") }
