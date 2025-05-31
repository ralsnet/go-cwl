package cwl

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"
)

const (
	fps = 30
)

type RenderParameter struct {
	Row, Col      int
	Width, Height int
}

type App struct {
	mu       sync.Mutex
	tty      *TTY
	screen   Screen
	logs     []*LogGroup
	selected []*LogGroup
	cfg      *Config
}

func NewApp() *App {
	tty, err := NewTTY()
	if err != nil {
		panic(err)
	}

	return &App{
		tty:    tty,
		screen: NewLoadingScreen(),
	}
}

func (a *App) ShowLoading(ctx context.Context) error {
	cfg, err := LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	a.cfg = cfg

	cfgs, err := LoadAWSConfigs(ctx, a.cfg.ExcludeProfiles)
	if err != nil {
		return err
	}

	a.logs, err = GetLogGroups(ctx, cfgs)
	if err != nil {
		return err
	}

	return a.ShowChooseLogsScreen(ctx)
}

func (a *App) ShowChooseLogsScreen(ctx context.Context) error {
	a.screen = NewChooseLogsScreen(a.logs, a.selected, func(selected []*LogGroup) error {
		a.selected = selected
		return a.ShowDisplayLogScreen(ctx, a.selected)
	})
	a.screen.Init(ctx)
	return nil
}

func (a *App) ShowDisplayLogScreen(ctx context.Context, logs []*LogGroup) error {
	a.screen = NewDisplayLogScreen(logs, func(logs []*LogGroup) {
		a.ShowChooseLogsScreen(ctx)
	})
	a.screen.Init(ctx)
	return nil
}

func (a *App) render(ctx context.Context) error {
	if !a.Opened() {
		return nil
	}

	if err := a.screen.Render(ctx, a.tty); err != nil {
		return err
	}
	return nil
}

func (a *App) handleInput(ctx context.Context, r rune) (bool, error) {
	return a.screen.HandleInput(ctx, r)
}

func (a *App) handleCtrl(ctx context.Context, ctrl string) (bool, error) {
	return a.screen.HandleCtrl(ctx, ctrl)
}

func (a *App) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	quit := false
	go func() {
		<-sig
		a.ForceUnlock()
		quit = true
		cancel()
		a.Close()
	}()

	defer func() {
		if err := recover(); err != nil {
			a.ForceUnlock()
			quit = true
			cancel()
			a.Close()
			fmt.Println(err)
		}
	}()

	if err := a.Open(); err != nil {
		return err
	}
	defer a.Close()

	go func() {
		if err := a.ShowLoading(ctx); err != nil {
			a.ForceUnlock()
			quit = true
			cancel()
			a.Close()
			fmt.Println(err)
		}
	}()

	go func() {
		ctrl := false
		ctrlCode := ""
		for !quit {
			r, err := a.tty.Rune()
			a.mu.Lock()
			if err != nil {
				a.mu.Unlock()
				return
			}

			if string(r) == "\x1b" {
				ctrl = true
				go func() {
					time.Sleep(time.Millisecond * 10)
					ctrl = false
				}()
				a.mu.Unlock()
				continue
			}
			if ctrl {
				ctrlCode += string(r)
				if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') {
					ctrl = false
					handled, err := a.handleCtrl(ctx, "\x1b"+ctrlCode)
					if err != nil {
						a.mu.Unlock()
						return
					}
					if !handled {
						a.mu.Unlock()
						return
					}
					ctrlCode = ""
				}
				a.mu.Unlock()
				continue
			}

			// Ctrl+C
			if r == 3 {
				a.mu.Unlock()
				return
			}

			handled, err := a.handleInput(ctx, r)
			a.mu.Unlock()
			if err != nil {
				return
			}
			if !handled {
				return
			}

			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
	}()

	ticker := time.NewTicker(time.Second / fps)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		a.render(ctx)
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			continue
		}
	}
}

func (a *App) Open() error {
	if err := a.tty.Open(); err != nil {
		return err
	}
	return nil
}

func (a *App) Close() error {
	a.ForceUnlock()
	a.tty.Close()
	return nil
}

func (a *App) Opened() bool {
	return a.tty.t != nil
}

func (a *App) ForceUnlock() {
	if !a.mu.TryLock() {
		a.mu.Unlock()
	}
}
