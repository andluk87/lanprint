package main

import (
	"flag"
	"fmt"
	"github.com/andluk87/lanprint/internal/core"
	"github.com/andluk87/lanprint/internal/gui"
	"github.com/andluk87/lanprint/internal/platform"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"
)

func main() {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root, _ = os.UserConfigDir()
	}
	root = filepath.Join(root, "LANPrint")
	config := flag.String("config", filepath.Join(root, "config.json"), "configuration path")
	headless := flag.Bool("headless", false, "run without GUI")
	autostart := flag.Bool("autostart", false, "restore services at login")
	version := flag.Bool("version", false, "show version")
	flag.Parse()
	if *version {
		fmt.Println(core.Version)
		return
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		log.Fatal(e)
	}
	messages := make(chan string, 256)
	var mu sync.Mutex
	logger := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		path := filepath.Join(root, "lanprint.log")
		if st, e := os.Stat(path); e == nil && st.Size() > 2<<20 {
			os.Remove(path + ".1")
			os.Rename(path, path+".1")
		}
		line := time.Now().Format(time.RFC3339) + " " + s
		f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if e == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
		select {
		case messages <- line:
		default:
		}
	}
	c, e := core.Load(*config)
	if e != nil {
		log.Fatal(e)
	}
	if _, e = os.Stat(*config); os.IsNotExist(e) {
		if e = core.Save(*config, c); e != nil {
			log.Fatal(e)
		}
	}
	if *headless {
		s, e := core.Start(c, platform.Backend{}, logger)
		if e != nil {
			log.Fatal(e)
		}
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
		s.Stop()
		return
	}
	if e = gui.Run(c, *config, logger, messages, *autostart); e != nil {
		log.Fatal(e)
	}
}
