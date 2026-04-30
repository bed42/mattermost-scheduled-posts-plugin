package main

import (
	"time"

	"github.com/pkg/errors"
)

type configuration struct {
	PollIntervalSeconds int
	MaxAttempts         int
	DefaultTimezone     string
}

func (p *Plugin) getConfiguration() *configuration {
	v := p.configuration.Load()
	if v == nil {
		return &configuration{PollIntervalSeconds: 30, MaxAttempts: 3, DefaultTimezone: "UTC"}
	}
	cfg, ok := v.(*configuration)
	if !ok || cfg == nil {
		return &configuration{PollIntervalSeconds: 30, MaxAttempts: 3, DefaultTimezone: "UTC"}
	}
	return cfg
}

func (p *Plugin) setConfiguration(c *configuration) {
	p.configuration.Store(c)
}

// OnConfigurationChange is called by the Mattermost server when the plugin
// configuration is updated. We snapshot the new values into an atomic.Value
// so the scheduler goroutine can read them without locking.
func (p *Plugin) OnConfigurationChange() error {
	cfg := &configuration{PollIntervalSeconds: 30, MaxAttempts: 3, DefaultTimezone: "UTC"}
	if err := p.API.LoadPluginConfiguration(cfg); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}
	if cfg.PollIntervalSeconds < 5 {
		cfg.PollIntervalSeconds = 5
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.DefaultTimezone == "" {
		cfg.DefaultTimezone = "UTC"
	}
	// Validate timezone — fall back to UTC if the admin enters something invalid.
	if _, err := time.LoadLocation(cfg.DefaultTimezone); err != nil {
		p.API.LogWarn("invalid DefaultTimezone, falling back to UTC",
			"value", cfg.DefaultTimezone, "err", err.Error())
		cfg.DefaultTimezone = "UTC"
	}
	p.setConfiguration(cfg)
	return nil
}

