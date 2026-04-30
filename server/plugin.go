package main

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/mattermost/mattermost-server/v6/plugin"
)

type Plugin struct {
	plugin.MattermostPlugin

	configuration atomic.Value

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func (p *Plugin) OnActivate() error {
	if err := p.OnConfigurationChange(); err != nil {
		return err
	}
	if err := p.API.RegisterCommand(p.scheduleCommand()); err != nil {
		return err
	}

	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	go p.runScheduler()
	return nil
}

func (p *Plugin) OnDeactivate() error {
	p.stopOnce.Do(func() {
		if p.stopCh != nil {
			close(p.stopCh)
		}
	})
	if p.doneCh != nil {
		<-p.doneCh
	}
	return nil
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.handleHTTP(w, r)
}
