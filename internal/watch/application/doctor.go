package application

import (
	"context"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// SourceCheck reads the repository's source section as register would,
// tracker included, and answers what it found or why it cannot.
func (s *Service) SourceCheck(ctx context.Context, dir string) (SourceFound, error) {
	in := RegisterInput{Dir: dir}
	src, err := s.readSource(ctx, in)
	if err != nil {
		return SourceFound{}, err
	}
	repo, err := s.selectRepo(ctx, Selector{Dir: dir})
	if err != nil {
		return src.found, err
	}
	forge, err := s.accounts.ForForge(ctx, repo)
	if err != nil {
		return src.found, err
	}
	if err := domain.CheckForge(src.read, repo, forge.Service); err != nil {
		return src.found, err
	}
	_, err = s.resolveTracker(in, &src, repo, forge.Service)
	return src.found, err
}

// AdapterOf answers the adapter a session loaded, if any.
func (s *Service) AdapterOf(ctx context.Context, session string) (*Adapter, error) {
	var a *Adapter
	err := s.store.Tx(ctx, func(tx Tx) error {
		var err error
		a, err = tx.Adapter(session)
		return err
	})
	return a, err
}
