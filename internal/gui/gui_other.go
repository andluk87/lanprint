//go:build !windows

package gui

import (
	"errors"
	"github.com/andluk87/lanprint/internal/core"
)

func Run(core.Config, string, func(string), <-chan string, bool) error {
	return errors.New("GUI available only on Windows")
}
