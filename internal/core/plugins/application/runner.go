package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

type Execution interface {
	Context() context.Context
	Output() []byte
	Close()
}
type Runtime interface {
	Acquire() (func(), error)
	Start(context.Context, *domain.Package, []byte, int) (Execution, error)
	Close() error
	Pause() func()
}
type Runner struct{ runtime Runtime }

func NewRunner(runtime Runtime) *Runner { return &Runner{runtime: runtime} }
func (r *Runner) Close() error          { return r.runtime.Close() }
func (r *Runner) Pause() func()         { return r.runtime.Pause() }
func (r *Runner) Run(ctx context.Context, store *Store, p *domain.Package, view string, input map[string]string) (domain.Result, error) {
	release, err := r.runtime.Acquire()
	if err != nil {
		return domain.Result{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var action *Action
	if p.Manifest.Storage != "" {
		if store == nil {
			return domain.Result{}, domain.ErrStorageDenied
		}
		readOnly := false
		for _, v := range p.Manifest.Views {
			if v.ID == view {
				readOnly = v.ReadOnly
			}
		}
		action, err = store.Begin(ctx, p, readOnly)
		if err != nil {
			return domain.Result{}, err
		}
		ctx = WithAction(ctx, action)
	}
	request, _ := json.Marshal(struct {
		View  string            `json:"view"`
		Input map[string]string `json:"input"`
	}{view, input})
	limit, textLimit := domain.MaxOutput, 12000
	if p.Manifest.Collection != nil {
		limit = 32 << 10
		textLimit = 16 << 10
	}
	execution, err := r.runtime.Start(ctx, p, request, limit)
	if err != nil {
		if action != nil && action.Failure() != nil {
			return domain.Result{}, action.Failure()
		}
		return domain.Result{}, err
	}
	defer execution.Close()
	var result domain.Result
	if err := domain.Decode(execution.Output(), &result); err != nil || !domain.Text(result.Text, textLimit) {
		return domain.Result{}, fmt.Errorf("invalid plugin output")
	}
	if action != nil {
		if err := action.Commit(execution.Context()); err != nil {
			return domain.Result{}, err
		}
	}
	return result, nil
}
