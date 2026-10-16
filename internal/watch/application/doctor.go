package application

import "context"

// SourceCheck reads the repository's source section as register would,
// and answers what it found or why it cannot.
func (s *Service) SourceCheck(ctx context.Context, dir string) (SourceFound, error) {
	_, _, found, err := s.readSource(ctx, RegisterInput{Dir: dir})
	return found, err
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
