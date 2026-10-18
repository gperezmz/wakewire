package fakes_test

import (
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/v2/internal/watch/application/storetest"
)

func TestFakeStoreKeepsTheContract(t *testing.T) {
	storetest.Run(t, func(*testing.T) application.Store { return fakes.NewStore() })
}
