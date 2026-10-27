package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func Run() {
	if os.Getenv("BANSHIT_DEMO") == "1" {
		if e := runDemo(); e != nil {
			log.Fatal(e)
		}
		return
	}
	a, e := NewApp(env("BANSHIT_CONFIG", "config.json"), env("BANSHIT_DATA", "data"))
	if e != nil {
		log.Fatal(e)
	}
	a.StartScheduler()
	c := a.snapshot()
	s := &http.Server{Addr: fmt.Sprintf("%s:%d", c.Host, c.Port), Handler: a, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		a.cancel()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdown)
	}()
	log.Printf("BanShit listening on %s", s.Addr)
	if e = s.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Print(e)
	}
	a.Close()
}
