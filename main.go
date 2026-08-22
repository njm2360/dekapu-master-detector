package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/njm2360/dekapu-master-detector/internal/detector"
	"github.com/njm2360/dekapu-master-detector/internal/oscout"
	"github.com/njm2360/dekapu-master-detector/internal/tail"
)

const (
	oscHost        = "127.0.0.1"
	oscPort        = 9000
	oscParam       = "/avatar/parameters/IsInstanceMaster"
	resendInterval = 3 * time.Second
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	logDir := filepath.Join(os.Getenv("USERPROFILE"), "AppData", "LocalLow", "VRChat", "VRChat")
	det := detector.New()
	sender, err := oscout.New(oscHost, oscPort, oscParam)
	if err != nil {
		log.Fatalf("osc: %v", err)
	}

	// リプレイ中の状態のバタつきを外部へ出さないよう、公開済みの値を別に持つ
	var published atomic.Bool
	send := func(v bool) {
		published.Store(v)
		if err := sender.Send(v); err != nil {
			log.Printf("osc: send failed: %v", err)
		}
	}

	live := false
	det.OnEvent = func(ev detector.Event) {
		if !live {
			return
		}
		log.Printf("event: %s (log time %s)", ev.Type, ev.Time.Format("15:04:05"))
	}
	checkPublish := func() {
		if !live {
			return
		}
		if cur := det.IsMaster(); cur != published.Load() {
			log.Printf("state changed: IsMaster=%v", cur)
			send(cur)
		}
	}

	go func() {
		t := time.NewTicker(resendInterval)
		defer t.Stop()
		for range t.C {
			if err := sender.Send(published.Load()); err != nil {
				log.Printf("osc: resend failed: %v", err)
			}
		}
	}()

	cb := tail.Callbacks{
		OnFileSwitch: func(path string) {
			live = false
			det.Reset()
		},
		OnLine: func(ts time.Time, msg string) {
			det.Feed(ts, msg)
			checkPublish()
		},
		OnCaughtUp: func() {
			live = true
			log.Printf("caught up (LIVE): state=%s", det.State())
			send(det.IsMaster())
		},
		OnTick: func(now time.Time) {
			det.Tick(now)
			checkPublish()
		},
		OnStale: func() {
			live = false
			det.Reset()
			log.Printf("state=UNKNOWN, sending false")
			send(false)
		},
	}

	log.Printf("log dir: %s", logDir)
	log.Printf("osc target: %s:%d %s", oscHost, oscPort, oscParam)
	if err := tail.Run(context.Background(), logDir, cb); err != nil {
		log.Fatal(err)
	}
}
