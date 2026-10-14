package main

import (
	"context"
	"errors"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/regeda/envoyadmin/internal/admin"
	"github.com/regeda/envoyadmin/internal/config"
	"github.com/regeda/envoyadmin/internal/demo"
	"github.com/regeda/envoyadmin/internal/snapshot"
	"github.com/regeda/envoyadmin/internal/ui"
)

func run(ctx context.Context, cfg config.Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if cfg.Demo {
		sc := demo.Degraded()
		sc.Updated = time.Now().UTC() // certificate expiries derive from Updated, so the demo never ages out
		srv := demo.NewServer(sc)
		url, stop, err := srv.Start()
		if err != nil {
			return err
		}
		defer stop()
		cfg.Admin = url
		go srv.Jitter(ctx, 2*cfg.Refresh)
	}

	client, err := admin.NewHTTP(cfg.Admin, cfg.Timeout)
	if err != nil {
		return err
	}
	model := ui.New(ui.Options{
		Ctx:    ctx,
		Config: cfg,
		Fetch:  func(ctx context.Context) (snapshot.Snapshot, error) { return snapshot.Fetch(ctx, client) },
		// Color gates only the chroma YAML highlighting; lipgloss/termenv honours NO_COLOR itself.
		Color: os.Getenv("NO_COLOR") == "",
	})
	_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
