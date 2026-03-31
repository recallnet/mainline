package app

import (
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/queue"
	"github.com/recallnet/mainline/internal/state"
	"github.com/recallnet/mainline/internal/worker"
)

type wiring struct {
	Git     git.Engine
	Queue   queue.Manager
	State   state.Store
	Policy  policy.Config
	Workers worker.Registry
}

func bootstrap() wiring {
	return wiring{
		Git:     git.NewEngine("."),
		Queue:   queue.NewManager(),
		State:   state.NewStore(""),
		Policy:  policy.DefaultConfig(),
		Workers: worker.NewRegistry(),
	}
}
